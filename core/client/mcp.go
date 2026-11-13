package client

import (
	"omixor-core/model"
	"omixor-core/service"
)

// 外部 MCP 閘道的登記(見 core/api/mcp.go;沒有任何密鑰)

func (c *Client) mcpURL(org string) string { return "/api/orgs/" + esc(org) + "/mcp-servers" }

// GetMCP 組織的外部 MCP 登記(成員;功能沒開時 Enabled = false、清單是空的)
func (c *Client) GetMCP(org string) (*service.MCPView, error) {
	var x service.MCPView
	return &x, c.Do("GET", c.mcpURL(org), nil, &x)
}

// SaveMCPServer 新增或修改一台伺服器(組織管理員)
func (c *Client) SaveMCPServer(org string, in service.MCPServerInput) (*model.MCPServerReg, error) {
	var x model.MCPServerReg
	return &x, c.Do("POST", c.mcpURL(org), in, &x)
}

// DeleteMCPServer 刪除一台伺服器(組織管理員)
func (c *Client) DeleteMCPServer(org, id string) error {
	return c.Do("POST", c.mcpURL(org)+"/"+esc(id)+"/delete", map[string]any{}, nil)
}

// SaveMCPTools 同步工具清單、改規則(組織管理員)
func (c *Client) SaveMCPTools(org, id string, in service.MCPToolsInput) (*model.MCPServerReg, error) {
	var x model.MCPServerReg
	return &x, c.Do("POST", c.mcpURL(org)+"/"+esc(id)+"/tools", in, &x)
}

// SaveMCPGrants 整份取代角色的權限(組織管理員)
func (c *Client) SaveMCPGrants(org string, in service.MCPGrantsInput) error {
	return c.Do("POST", "/api/orgs/"+esc(org)+"/mcp-grants", in, nil)
}

// ResolveMCPTools 判斷工具能不能用(成員)
func (c *Client) ResolveMCPTools(org string, in service.MCPResolveInput) (*service.MCPResolveResult, error) {
	var x service.MCPResolveResult
	return &x, c.Do("POST", "/api/orgs/"+esc(org)+"/mcp-tools/resolve", in, &x)
}
