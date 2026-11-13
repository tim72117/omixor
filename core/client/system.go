package client

import "omixor-core/service"

// 組織的系統設定(功能開關;見 core/api/system.go)

func (c *Client) systemPath(org string) string { return "/api/orgs/" + esc(org) + "/system" }

// GetSystemSettings 功能清單與這個組織目前的開關(成員)
func (c *Client) GetSystemSettings(org string) (*service.SystemSettingsView, error) {
	var x service.SystemSettingsView
	return &x, c.Do("GET", c.systemPath(org), nil, &x)
}

// SaveSystemSettings 修改功能開關(組織管理員)
func (c *Client) SaveSystemSettings(org string, in service.SystemSettingsInput) (*service.SystemSettingsView, error) {
	var x service.SystemSettingsView
	return &x, c.Do("POST", c.systemPath(org), in, &x)
}
