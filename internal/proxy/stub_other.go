//go:build !windows

package proxy

import "errors"

type Service struct{}

func New(string, func(string)) *Service { return &Service{} }

func (s *Service) SetSaveDir(string) {}

func (s *Service) Start() error { return errors.New("sphvideo supports Windows only") }

func (s *Service) Stop() {}

func RunWatchdog(string) {}
