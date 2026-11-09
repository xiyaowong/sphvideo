//go:build !windows

package main

func requireAdmin() bool { return false }
