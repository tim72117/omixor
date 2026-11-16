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

// GetServerFeatures 系統層的功能設定(整台伺服器;任何登入的使用者)
func (c *Client) GetServerFeatures() (*service.ServerFeaturesView, error) {
	var x service.ServerFeaturesView
	return &x, c.Do("GET", "/api/system/features", nil, &x)
}

// SaveServerFeatures 修改系統層的功能開關(伺服器管理員)
func (c *Client) SaveServerFeatures(in service.SystemSettingsInput) (*service.ServerFeaturesView, error) {
	var x service.ServerFeaturesView
	return &x, c.Do("POST", "/api/system/features", in, &x)
}
