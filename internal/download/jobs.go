package download

import (
	"context"
	"crypto/rand"
	"encoding/hex"
	"errors"
	"fmt"
	"net"
	"os"
	"strings"
	"sync"
	"time"
)

type JobState string

const (
	JobRunning  JobState = "running"
	JobDone     JobState = "done"
	JobError    JobState = "error"
	JobCanceled JobState = "canceled"
)

type JobSnapshot struct {
	ID       string   `json:"id"`
	Kind     string   `json:"kind"`
	Title    string   `json:"title,omitempty"`
	State    JobState `json:"state"`
	Progress float64  `json:"progress"`
	Written  int64    `json:"written"`
	Total    int64    `json:"total"`
	Detail   string   `json:"detail,omitempty"`
	Path     string   `json:"path,omitempty"`
	Error    string   `json:"error,omitempty"`
}

type job struct {
	mu sync.Mutex
	JobSnapshot
	cancel context.CancelFunc
}

func (j *job) report(written, total int64, detail string) {
	j.mu.Lock()
	defer j.mu.Unlock()
	j.Written = written
	j.Total = total
	switch {
	case total > 0:
		j.Progress = float64(written) / float64(total)
		if j.Progress > 1 {
			j.Progress = 1
		}
	case written > 0:
		j.Progress = -1
	default:
		j.Progress = 0
	}
	if detail != "" {
		j.Detail = detail
	}
}

func (j *job) snapshot() JobSnapshot {
	j.mu.Lock()
	defer j.mu.Unlock()
	return j.JobSnapshot
}

type Manager struct {
	mu     sync.Mutex
	jobs   map[string]*job
	order  []string
	keep   int
	onDone func(JobSnapshot)
}

func NewManager() *Manager {
	return &Manager{jobs: make(map[string]*job), keep: 20}
}

func (m *Manager) SetOnDone(fn func(JobSnapshot)) {
	m.mu.Lock()
	m.onDone = fn
	m.mu.Unlock()
}

func (m *Manager) Start(req Request, dir string, proxy proxyFunc) JobSnapshot {
	id := newJobID()
	ctx, cancel := context.WithCancel(context.Background())
	j := &job{
		JobSnapshot: JobSnapshot{
			ID:     id,
			Kind:   req.Kind,
			Title:  req.Title,
			State:  JobRunning,
			Detail: "准备中…",
		},
		cancel: cancel,
	}
	m.mu.Lock()
	m.jobs[id] = j
	m.order = append(m.order, id)
	m.pruneLocked()
	m.mu.Unlock()

	go func() {
		path, err := Save(ctx, req, dir, proxy, j.report)
		j.mu.Lock()
		switch {
		case err == nil:
			j.State = JobDone
			j.Path = path
			j.Detail = ""
			j.Progress = 1
			j.Written = j.Total
			if j.Total == 0 {
				if st, statErr := os.Stat(path); statErr == nil {
					j.Total = st.Size()
					j.Written = st.Size()
				}
			}
		case errors.Is(err, context.Canceled):
			j.State = JobCanceled
			j.Error = "已取消"
			j.Progress = 0
			j.Detail = ""
		default:
			j.State = JobError
			j.Error = FriendlyError(err)
			j.Progress = 0
			j.Detail = ""
		}
		snap := j.JobSnapshot
		j.mu.Unlock()

		m.mu.Lock()
		fn := m.onDone
		m.mu.Unlock()
		if fn != nil {
			fn(snap)
		}
	}()

	return j.snapshot()
}

func (m *Manager) Get(id string) (JobSnapshot, bool) {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok {
		return JobSnapshot{}, false
	}
	return j.snapshot(), true
}

func (m *Manager) Cancel(id string) bool {
	m.mu.Lock()
	j, ok := m.jobs[id]
	m.mu.Unlock()
	if !ok || j.snapshot().State != JobRunning {
		return false
	}
	j.cancel()
	return true
}

func (m *Manager) CancelAll() {
	m.mu.Lock()
	var cancels []context.CancelFunc
	for _, j := range m.jobs {
		if j.snapshot().State == JobRunning {
			cancels = append(cancels, j.cancel)
		}
	}
	m.mu.Unlock()
	for _, cancel := range cancels {
		cancel()
	}
}

func (m *Manager) Active() []JobSnapshot {
	m.mu.Lock()
	defer m.mu.Unlock()
	active := make([]JobSnapshot, 0, 2)
	for _, id := range m.order {
		if j, ok := m.jobs[id]; ok {
			if snap := j.snapshot(); snap.State == JobRunning {
				active = append(active, snap)
			}
		}
	}
	return active
}

func (m *Manager) pruneLocked() {
	excess := len(m.order) - m.keep
	if excess <= 0 {
		return
	}
	kept := m.order[:0]
	for _, id := range m.order {
		if excess > 0 {
			if j, ok := m.jobs[id]; ok && j.snapshot().State != JobRunning {
				delete(m.jobs, id)
				excess--
				continue
			}
		}
		kept = append(kept, id)
	}
	m.order = kept
}

func newJobID() string {
	var b [8]byte
	if _, err := rand.Read(b[:]); err != nil {
		return fmt.Sprintf("%d", time.Now().UnixNano())
	}
	return hex.EncodeToString(b[:])
}

type userError struct {
	msg string
	err error
}

func (e *userError) Error() string { return e.msg }

func (e *userError) Unwrap() error { return e.err }

func userErr(msg string, err error) error { return &userError{msg: msg, err: err} }

// FriendlyError turns an internal error into a sentence a user can act on.
func FriendlyError(err error) string {
	if err == nil {
		return ""
	}
	if errors.Is(err, context.Canceled) {
		return "已取消"
	}
	if errors.Is(err, context.DeadlineExceeded) {
		return "网络超时，请检查网络后重试"
	}
	var ue *userError
	if errors.As(err, &ue) {
		return ue.msg
	}
	if errors.Is(err, os.ErrPermission) {
		return "无法写入保存目录，请更换保存目录"
	}
	var ne net.Error
	if errors.As(err, &ne) && ne.Timeout() {
		return "网络超时，请检查网络后重试"
	}
	if isNoSpace(err) {
		return "磁盘空间不足，请清理后重试"
	}
	var pe *os.PathError
	if errors.As(err, &pe) {
		return "无法保存文件，请更换保存目录"
	}
	if strings.Contains(strings.ToLower(err.Error()), "connection") {
		return "网络中断，请检查网络后重试"
	}
	return "操作失败，请重试"
}
