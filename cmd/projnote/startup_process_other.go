//go:build !linux && !windows

package main

func prepareDesktopProcess() error                                          { return nil }
func startDesktopBackground(string, desktopBackgroundOptions) (bool, error) { return false, nil }
func desktopServiceReady(string)                                            {}
func desktopProcessDone(error)                                              {}
func presentStartupFailure(string)                                          {}
