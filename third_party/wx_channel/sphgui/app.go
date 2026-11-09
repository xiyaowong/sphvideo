// Package sphgui starts the video-channel interception, page runtime, local
// API, and download engine of wx_channels_download behind the sphvideo GUI.
package sphgui

import (
	"context"
	"fmt"
	"net"
	"net/http"
	"os"
	"path/filepath"
	"sort"
	"strconv"
	"sync"
	"time"

	"github.com/gin-gonic/gin"
	"github.com/ltaoo/velo"
	"github.com/rs/zerolog"
	"github.com/spf13/viper"
	"gorm.io/gorm"

	"wx_channel/internal/adapter"
	wxchannelsadapter "wx_channel/internal/adapter/wxchannels"
	"wx_channel/internal/api"
	"wx_channel/internal/config"
	"wx_channel/internal/database"
	"wx_channel/internal/events"
	"wx_channel/internal/interceptor"
	"wx_channel/internal/services"
	"wx_channel/internal/webassets"
	"wx_channel/pkg/certificate"
	"wx_channel/pkg/hermes"
	"wx_channel/pkg/hermes/protocol"
)

// Options configures one run of the video-channel service.
type Options struct {
	Version   string
	SaveDir   string
	APIPort   int
	ProxyPort int
	CertPEM   []byte
	KeyPEM    []byte

	OnDownload      func(DownloadEvent)
	OnPageConnected func(bool)
}

// Progress is the GUI-facing state of one running download task.
type Progress struct {
	TaskID     int
	Title      string
	Detail     string
	Downloaded int64
	Total      int64
	Speed      int64
	Fraction   float64
}

// DownloadEvent reports a finished or failed download.
type DownloadEvent struct {
	Finished bool
	TaskID   int
	Title    string
	Path     string
	Error    string
}

// Snapshot is the GUI-facing state of the service.
type Snapshot struct {
	Running       bool
	PageConnected bool
	Active        []Progress
	LastResult    string
	LastError     string
}

// App owns one running service instance.
type App struct {
	opts Options

	cfg         *config.Config
	db          *gorm.DB
	sqlDB       interface{ Close() error }
	taskStore   *database.DBTaskStore
	downloader  *hermes.HermesEngine
	taskService *services.DownloadTaskService
	broadcaster *api.DownloadTaskBroadcaster
	apiClient   *api.APIClient
	httpServer  *http.Server
	httpLn      net.Listener
	interceptor *interceptor.InterceptorServer
	adapter     adapter.RuntimeHandle
	bus         *events.Bus

	mu            sync.Mutex
	running       bool
	pageConnected bool
	active        map[int]Progress
	activeIDs     map[int]struct{}
	lastResult    string
	lastError     string
}

// Start brings up the API, injects the page runtime, and starts the proxy.
func Start(opts Options) (*App, error) {
	if len(opts.CertPEM) == 0 || len(opts.KeyPEM) == 0 {
		return nil, fmt.Errorf("证书数据为空")
	}
	if opts.APIPort <= 0 {
		opts.APIPort = 2022
	}
	if opts.ProxyPort <= 0 {
		opts.ProxyPort = 2023
	}
	if opts.SaveDir == "" {
		return nil, fmt.Errorf("保存目录为空")
	}

	cfg, err := newConfig(opts)
	if err != nil {
		return nil, err
	}

	box := velo.NewApp(&velo.VeloAppOpt{Mode: velo.ModeHttp})
	if err := box.Migrate(&velo.VeloDatabaseOpt{
		DBType:                    velo.DBTypeSQLite,
		DBPath:                    ":memory:",
		Migrations:                &database.Migrations,
		DisableTimestampCallbacks: true,
	}); err != nil {
		return nil, fmt.Errorf("初始化下载数据库失败: %w", err)
	}
	sqlDB, err := box.DB.DB()
	if err != nil {
		return nil, fmt.Errorf("初始化下载数据库失败: %w", err)
	}
	// A single connection keeps every goroutine on the same in-memory database.
	sqlDB.SetMaxOpenConns(1)
	sqlDB.SetMaxIdleConns(1)

	a := &App{
		opts:      opts,
		cfg:       cfg,
		db:        box.DB,
		sqlDB:     sqlDB,
		bus:       events.NewBus(),
		active:    make(map[int]Progress),
		activeIDs: make(map[int]struct{}),
	}
	if err := a.setup(); err != nil {
		a.closeResources()
		return nil, err
	}
	return a, nil
}

func newConfig(opts Options) (*config.Config, error) {
	exe, err := os.Executable()
	if err != nil {
		return nil, err
	}
	exeDir := filepath.Dir(exe)
	// Point the loader at a path that never exists so no config file is read
	// or written; every value below lives in memory for this run only.
	_ = os.Setenv(config.EnvConfigPath, filepath.Join(exeDir, ".sphvideo-runtime.yaml"))

	logger := zerolog.Nop()
	cfg := config.New(opts.Version, "release", &logger, nil, "")

	viper.Set("workdir", exeDir)
	viper.Set("db.type", "sqlite")
	viper.Set("db.filepath", ":memory:")

	viper.Set("api.protocol", "http")
	viper.Set("api.hostname", "127.0.0.1")
	viper.Set("api.port", opts.APIPort)

	viper.Set("proxy.enabled", true)
	viper.Set("proxy.system", false)
	viper.Set("proxy.hostname", "127.0.0.1")
	viper.Set("proxy.port", opts.ProxyPort)
	viper.Set("proxy.tun", false)
	viper.Set("proxy.tcpRelay.enabled", false)
	viper.Set("proxy.skipInstallRootCert", true)
	viper.Set("proxy.upstreamProxy", "")

	viper.Set("channels.disableLocationToHome", false)
	viper.Set("channels.download.defaultHighest", false)
	viper.Set("channels.download.frontend", false)
	viper.Set("channels.download.cover", false)
	viper.Set("channels.download.pauseWhenDownload", false)
	viper.Set("channels.download.forceCheckAllFeeds", false)

	viper.Set("download.dir", opts.SaveDir)
	viper.Set("download.filenameTemplate", "{{filename}}")
	viper.Set("download.maxRunning", 3)
	viper.Set("download.playDoneAudio", false)
	viper.Set("debug.error", false)
	viper.Set("mcp.enabled", false)

	if err := cfg.LoadConfig(); err != nil {
		return nil, fmt.Errorf("初始化配置失败: %w", err)
	}
	return cfg, nil
}

func (a *App) setup() error {
	logger := a.cfg.Logger()
	downloadDir := a.cfg.GetString("download.dir")

	a.taskStore = database.NewDBTaskStore(a.db, logger)
	hookManager := hermes.NewHookManager()
	a.downloader = hermes.New(hermes.HermesNewConfig{
		Store:  a.taskStore,
		Logger: logger,
		Config: hermes.HermesEngineConfig{
			MaxConcurrent:         3,
			ResourceConcurrency:   100,
			SegmentConcurrency:    10,
			ConnectionConcurrency: 0,
			BasePath:              downloadDir,
		},
	})
	a.downloader.RegisterProtocol(protocol.NewHTTPDriver())
	a.downloader.RegisterProtocol(protocol.NewStreamDriver())
	a.downloader.RegisterProtocol(protocol.NewInlineDriver())
	a.downloader.RegisterProtocol(protocol.NewFileDriver())
	a.downloader.SetHooks(hookManager)
	a.downloader.SetPostprocessor(adapter.NewPlatformPostprocessor(a.db, *logger, downloadDir))
	a.downloader.OnEvent(a.onHermesEvent)

	a.taskService = services.NewDownloadTaskService(
		a.db,
		logger,
		a.downloader,
		hookManager,
		a.cfg.WorkDir,
		downloadDir,
		a.bus,
	)
	a.broadcaster = api.NewDownloadTaskBroadcaster(a.db, logger, a.taskService)

	apiCfg := api.NewAPIConfig(a.cfg)
	runtimeStatus := services.NewRuntimeStatusService()
	a.apiClient = api.NewSphAPIClient(
		apiCfg,
		logger,
		a.db,
		webassets.NewRegistry(),
		a.broadcaster,
		a.bus,
		runtimeStatus,
		a.taskService,
		services.NewFSService(),
		services.NewBrowseService(a.db, *logger),
	)

	a.bus.Subscribe(events.TypeServiceStatusChanged, func(event events.Event) {
		if status, ok := event.(events.ServiceStatusChanged); ok {
			runtimeStatus.UpdateServiceStatus(status)
		}
	})
	a.bus.Subscribe(events.TypeProxyStatusChanged, func(event events.Event) {
		if status, ok := event.(events.ProxyStatusChanged); ok {
			runtimeStatus.UpdateProxyStatus(status)
		}
	})
	a.bus.Subscribe(events.TypePlatformStatusChanged, func(event events.Event) {
		status, ok := event.(events.PlatformStatusChanged)
		if !ok || status.Platform != wxchannelsadapter.PlatformID || status.Key != "wxchannels:page" {
			return
		}
		a.setPageConnected(status.Available)
	})
	a.bus.Subscribe(events.TypeDownloadTaskCreated, func(event events.Event) {
		if created, ok := event.(events.DownloadTaskCreated); ok {
			a.broadcaster.NotifyCreated(created.TaskID)
		}
	})
	a.bus.Subscribe(events.TypeDownloadTaskDeleted, func(event events.Event) {
		if deleted, ok := event.(events.DownloadTaskDeleted); ok {
			a.broadcaster.NotifyDeleted(deleted.TaskID)
		}
	})

	certFiles := &certificate.CertFileAndKeyFile{
		Name:       "SphVideo Temp CA",
		Cert:       a.opts.CertPEM,
		PrivateKey: a.opts.KeyPEM,
	}
	a.interceptor = interceptor.NewInterceptorServer(a.cfg, certFiles, logger)
	a.interceptor.SubscribeEvents(a.bus)

	handler := adapter.Get(wxchannelsadapter.PlatformID)
	channels, ok := handler.(*wxchannelsadapter.ChannelsAdapter)
	if !ok {
		return fmt.Errorf("视频号适配器未注册")
	}
	runtimeHandle, err := channels.RegisterRuntime(&adapter.AdapterOptions{
		StaticAssets: webassets.NewRegistry(),
		Routes:       routeRegistrar{engine: a.apiClient.Engine()},
		Interceptor:  a.interceptor.Interceptor,
		DB:           a.db,
		Logger:       logger,
		Bus:          a.bus,
		Config:       a.cfg,
		Hooks:        hookManager,
	})
	if err != nil {
		return fmt.Errorf("初始化视频号适配器失败: %w", err)
	}
	a.adapter = runtimeHandle

	addr := net.JoinHostPort(a.cfg.GetString("api.hostname"), strconv.Itoa(a.cfg.GetInt("api.port")))
	listener, err := net.Listen("tcp", addr)
	if err != nil {
		return fmt.Errorf("本地端口被占用: %w", err)
	}
	a.httpLn = listener
	a.httpServer = &http.Server{Addr: addr, Handler: a.apiClient.SphHTTPHandler()}
	go func() {
		_ = a.httpServer.Serve(listener)
	}()

	if err := a.interceptor.Start(); err != nil {
		return fmt.Errorf("启动代理失败: %w", err)
	}

	a.mu.Lock()
	a.running = true
	a.mu.Unlock()
	return nil
}

// Stop shuts the service down. System proxy and certificate cleanup are the
// caller's responsibility and must run after Stop returns.
func (a *App) Stop() {
	if a == nil {
		return
	}
	a.mu.Lock()
	if !a.running {
		a.mu.Unlock()
		return
	}
	ids := make([]int, 0, len(a.activeIDs))
	for id := range a.activeIDs {
		ids = append(ids, id)
	}
	a.running = false
	a.mu.Unlock()

	for _, id := range ids {
		a.downloader.DeleteTask(id)
	}
	if a.interceptor != nil {
		_ = a.interceptor.Stop()
	}
	if a.apiClient != nil {
		_ = a.apiClient.Stop()
	}
	if a.httpServer != nil {
		ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
		_ = a.httpServer.Shutdown(ctx)
		cancel()
		_ = a.httpServer.Close()
		a.httpServer = nil
	}
	if a.httpLn != nil {
		_ = a.httpLn.Close()
		a.httpLn = nil
	}
	if a.adapter != nil {
		a.adapter.Stop()
		a.adapter = nil
	}
	a.closeResources()
}

func (a *App) closeResources() {
	if a.sqlDB != nil {
		_ = a.sqlDB.Close()
		a.sqlDB = nil
	}
}

// SetSaveDir changes the default output directory for new tasks.
func (a *App) SetSaveDir(dir string) {
	if a == nil || dir == "" {
		return
	}
	a.mu.Lock()
	a.opts.SaveDir = dir
	a.mu.Unlock()
	if a.taskService != nil {
		a.taskService.SphSetDownloadDir(dir)
	}
	if a.apiClient != nil {
		a.apiClient.SphSetDownloadDir(dir)
	}
}

// Snapshot returns the current state for the GUI.
func (a *App) Snapshot() Snapshot {
	if a == nil {
		return Snapshot{}
	}
	a.mu.Lock()
	defer a.mu.Unlock()
	active := make([]Progress, 0, len(a.active))
	for _, item := range a.active {
		active = append(active, item)
	}
	sort.Slice(active, func(i, j int) bool { return active[i].TaskID < active[j].TaskID })
	return Snapshot{
		Running:       a.running,
		PageConnected: a.pageConnected,
		Active:        active,
		LastResult:    a.lastResult,
		LastError:     a.lastError,
	}
}

func (a *App) setPageConnected(connected bool) {
	a.mu.Lock()
	changed := a.pageConnected != connected
	a.pageConnected = connected
	callback := a.opts.OnPageConnected
	a.mu.Unlock()
	if changed && callback != nil {
		callback(connected)
	}
}

func (a *App) onHermesEvent(event hermes.EventType, data hermes.EventData) {
	taskID, progress, finished, errMsg, ok := eventData(event, data)
	if !ok {
		return
	}
	if a.broadcaster != nil {
		a.broadcaster.Notify(taskID, event, progress, finished)
	}

	a.mu.Lock()
	switch event {
	case hermes.EventCreated, hermes.EventPreparing, hermes.EventStarted:
		a.activeIDs[taskID] = struct{}{}
		item := a.active[taskID]
		item.TaskID = taskID
		if item.Title == "" {
			item.Title = fmt.Sprintf("任务 %d", taskID)
		}
		item.Detail = "准备中…"
		item.Fraction = -1
		a.active[taskID] = item
	case hermes.EventProgress:
		if progress == nil {
			break
		}
		a.activeIDs[taskID] = struct{}{}
		item := a.active[taskID]
		item.TaskID = taskID
		item.Total = progress.TotalSize
		item.Downloaded = progress.Downloaded
		item.Speed = progress.Speed
		if progress.TotalSize > 0 {
			item.Fraction = float64(progress.Downloaded) / float64(progress.TotalSize)
			if item.Fraction > 1 {
				item.Fraction = 1
			}
		} else {
			item.Fraction = -1
		}
		item.Detail = ""
		for i := range progress.Resources {
			if progress.Resources[i].Name != "" {
				item.Title = progress.Resources[i].Name
				break
			}
		}
		if item.Title == "" {
			item.Title = fmt.Sprintf("任务 %d", taskID)
		}
		a.active[taskID] = item
	case hermes.EventPaused:
		delete(a.active, taskID)
		delete(a.activeIDs, taskID)
	case hermes.EventFinished:
		item := a.active[taskID]
		delete(a.active, taskID)
		delete(a.activeIDs, taskID)
		path := ""
		for i := range finished {
			if finished[i].FilePath != "" {
				path = finished[i].FilePath
				if item.Title == "" || item.Title == fmt.Sprintf("任务 %d", taskID) {
					item.Title = finished[i].Name
				}
				break
			}
		}
		if path != "" {
			a.lastResult = path
		}
		a.lastError = ""
		a.mu.Unlock()
		if callback := a.opts.OnDownload; callback != nil {
			callback(DownloadEvent{Finished: true, TaskID: taskID, Title: item.Title, Path: path})
		}
		return
	case hermes.EventFailed:
		item := a.active[taskID]
		delete(a.active, taskID)
		delete(a.activeIDs, taskID)
		a.lastError = errMsg
		a.mu.Unlock()
		if callback := a.opts.OnDownload; callback != nil {
			callback(DownloadEvent{Finished: false, TaskID: taskID, Title: item.Title, Error: errMsg})
		}
		return
	case hermes.EventDeleted:
		delete(a.active, taskID)
		delete(a.activeIDs, taskID)
	}
	a.mu.Unlock()
}

func eventData(event hermes.EventType, data hermes.EventData) (int, *hermes.TaskProgress, []hermes.TaskFinishedResource, string, bool) {
	switch event {
	case hermes.EventCreated:
		value, ok := data.(hermes.TaskCreatedEventData)
		return value.TaskID, nil, nil, "", ok
	case hermes.EventPreparing:
		value, ok := data.(hermes.TaskPreparingEventData)
		return value.TaskID, nil, nil, "", ok
	case hermes.EventStarted:
		value, ok := data.(hermes.TaskStartedEventData)
		return value.TaskID, nil, nil, "", ok
	case hermes.EventProgress:
		value, ok := data.(hermes.TaskProgressEventData)
		return value.TaskID, value.Progress, nil, "", ok
	case hermes.EventPaused:
		value, ok := data.(hermes.TaskPausedEventData)
		return value.TaskID, nil, nil, "", ok
	case hermes.EventFinished:
		value, ok := data.(hermes.TaskFinishedEventData)
		return value.TaskID, nil, value.Resources, "", ok
	case hermes.EventFailed:
		value, ok := data.(hermes.TaskFailedEventData)
		return value.TaskID, nil, nil, value.Error, ok
	case hermes.EventDeleted:
		value, ok := data.(hermes.TaskDeletedEventData)
		return value.TaskID, nil, nil, "", ok
	default:
		return 0, nil, nil, "", false
	}
}

type routeRegistrar struct {
	engine *gin.Engine
}

func (r routeRegistrar) RegisterGET(path string, handler gin.HandlerFunc) {
	if r.engine != nil && path == wxchannelsadapter.ChannelsWebsocketPath {
		r.engine.GET(path, handler)
	}
}

func (r routeRegistrar) RegisterPOST(string, gin.HandlerFunc) {}
