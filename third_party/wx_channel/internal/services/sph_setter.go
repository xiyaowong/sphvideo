package services

// SphSetDownloadDir changes the default directory for newly created tasks.
func (s *DownloadTaskService) SphSetDownloadDir(dir string) {
	if s == nil {
		return
	}
	s.download_dir = dir
}
