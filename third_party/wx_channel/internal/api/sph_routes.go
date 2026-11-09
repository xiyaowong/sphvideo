package api

import (
	"net/http"

	"github.com/gin-gonic/gin"
	"github.com/rs/zerolog"
	"gorm.io/gorm"

	"wx_channel/internal/events"
	"wx_channel/internal/services"
	"wx_channel/internal/webassets"
)

// NewSphAPIClient builds the API surface used by the injected video-channel
// page. The admin frontend and the other platform routes are not registered.
func NewSphAPIClient(
	cfg *APIConfig,
	logger *zerolog.Logger,
	db *gorm.DB,
	static_assets *webassets.Registry,
	broadcaster *DownloadTaskBroadcaster,
	bus events.Publisher,
	runtime_status_service *services.RuntimeStatusService,
	download_task_service *services.DownloadTaskService,
	fs_service *services.FSService,
	browse_history_service *services.BrowseService,
) *APIClient {
	gin.SetMode(gin.ReleaseMode)
	engine := gin.New()
	engine.Use(gin.Recovery())
	client := &APIClient{
		cfg:                       cfg,
		engine:                    engine,
		db:                        db,
		logger:                    logger,
		static_assets:             static_assets,
		download_task_broadcaster: broadcaster,
		event_publisher:           bus,
		runtime_status_service:    runtime_status_service,
		download_task_service:     download_task_service,
		fs_service:                fs_service,
		browse_history_service:    browse_history_service,
	}
	client.setupSphRoutes()
	return client
}

func (c *APIClient) setupSphRoutes() {
	c.engine.GET("/api/status", c.handle_status)
	c.engine.POST("/api/show_file", c.handle_show_file)
	c.engine.POST("/api/browse_history/create", c.handle_create_browse_history)
	c.engine.GET("/ws/v1/download_task", c.handle_download_task_ws)

	c.engine.POST("/api/v1/download_task/prepare", c.handle_prepare_download_task)
	c.engine.POST("/api/v1/download_task/prepare_by_url", c.handle_prepare_download_task_by_url)
	c.engine.POST("/api/v1/download_task/create", c.handle_create_download_task)
	c.engine.POST("/api/v1/download_task/create_by_url", c.handle_create_download_task_by_url)
	c.engine.POST("/api/v1/download_task/start", c.handle_start_download_task)
	c.engine.POST("/api/v1/download_task/pause", c.handle_pause_download_task)
	c.engine.POST("/api/v1/download_task/resume", c.handle_resume_download_task)
	c.engine.POST("/api/v1/download_task/retry", c.handle_retry_download_task)
	c.engine.POST("/api/v1/download_task/delete", c.handle_delete_download_task)
	c.engine.POST("/api/v1/download_task/start_all", c.handle_start_all_download_task)
	c.engine.POST("/api/v1/download_task/pause_all", c.handle_pause_all_download_task)
	c.engine.POST("/api/v1/download_task/clear", c.handle_clear_download_task)
	c.engine.POST("/api/v1/download_task/clear_all", c.handle_clear_all_download_task)
	c.engine.POST("/api/v1/download_task/check_files", c.handle_check_download_task_files)
	c.engine.GET("/api/v1/download_task/list", c.handle_list_download_task)
	c.engine.GET("/api/v1/download_task/detail", c.handle_download_task_detail)
}

// SphHTTPHandler wraps the API with CORS, including the private-network
// permission Chromium asks for when a public page calls a loopback service.
func (c *APIClient) SphHTTPHandler() http.Handler {
	inner := with_cors(c)
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Access-Control-Allow-Private-Network", "true")
		inner.ServeHTTP(w, r)
	})
}

// SphSetDownloadDir keeps the API default directory in step with the GUI.
func (c *APIClient) SphSetDownloadDir(dir string) {
	if c == nil || c.cfg == nil {
		return
	}
	c.cfg.DownloadDir = dir
}
