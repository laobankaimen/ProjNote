package main

import (
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strconv"
	"strings"
	"time"

	"projnote/internal/server"
)

type lanStartupOptions struct {
	enabled bool
	port    int
	persist bool
}

func selectLANStartup(enabled bool, port int, explicit, explicitPort bool, settings map[string]any) lanStartupOptions {
	option := lanStartupOptions{enabled: fmt.Sprint(settings["lan_enabled"]) == "1", port: server.DefaultLANPort, persist: explicit || explicitPort}
	if saved, err := strconv.Atoi(fmt.Sprint(settings["lan_port"])); err == nil && saved >= 1024 && saved <= 65436 {
		option.port = saved
	}
	if explicit {
		option.enabled = enabled
	}
	if explicitPort {
		option.port = port
	}
	return option
}
func requestExistingLAN(base string, enabled bool, port int) error {
	u, err := url.Parse(base)
	if err != nil || u.Scheme != "http" || u.Hostname() != "127.0.0.1" || u.Port() == "" || u.User != nil || u.Path != "" || u.RawQuery != "" || u.Fragment != "" {
		return errors.New("本机负责人实例地址无效")
	}
	payload := fmt.Sprintf(`{"enabled":%t,"port":%d}`, enabled, port)
	request, err := http.NewRequest("POST", base+"/api/lan/control", strings.NewReader(payload))
	if err != nil {
		return err
	}
	request.Header.Set("Content-Type", "application/json")
	request.Header.Set("X-ProjNote-Request", "1")
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Timeout: 15 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return errors.New("本机协同控制拒绝重定向") }}
	defer transport.CloseIdleConnections()
	response, err := client.Do(request)
	if err != nil {
		return err
	}
	defer response.Body.Close()
	var result struct {
		Data  map[string]any `json:"data"`
		Error struct {
			Message string `json:"message"`
		} `json:"error"`
	}
	if err = json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&result); err != nil {
		return fmt.Errorf("本机协同控制响应无效: %w", err)
	}
	if response.StatusCode != http.StatusOK {
		if result.Error.Message != "" {
			return errors.New(result.Error.Message)
		}
		return fmt.Errorf("局域网设置失败：HTTP %d", response.StatusCode)
	}
	if enabled && result.Data["running"] != true {
		return errors.New("局域网监听尚未确认运行，请在本机设置检查状态")
	}
	if !enabled && result.Data["running"] != false {
		return errors.New("局域网监听尚未确认关闭，请在本机设置检查状态")
	}
	fmt.Printf("局域网协同状态：%v，地址：%v\n", result.Data["running"], result.Data["addresses"])
	return nil
}

func existingLANPort(base string) int {
	transport := http.DefaultTransport.(*http.Transport).Clone()
	transport.Proxy = nil
	client := &http.Client{Timeout: 2 * time.Second, Transport: transport, CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }}
	defer transport.CloseIdleConnections()
	response, err := client.Get(base + "/api/settings")
	if err != nil {
		return server.DefaultLANPort
	}
	defer response.Body.Close()
	var value struct {
		Data map[string]any `json:"data"`
	}
	if response.StatusCode != 200 || json.NewDecoder(io.LimitReader(response.Body, 1<<20)).Decode(&value) != nil {
		return server.DefaultLANPort
	}
	if port, err := strconv.Atoi(fmt.Sprint(value.Data["lan_port"])); err == nil && port >= 1024 && port <= 65436 {
		return port
	}
	return server.DefaultLANPort
}
