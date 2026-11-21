package service

import "strings"

// 組織的 GitHub、GitLab 設定搬到選用模組 core/modules/integrations(service.go);這裡只留其他功能也在用的小工具。

// containsFold 不分大小寫找
func containsFold(list []string, s string) bool {
	for _, x := range list {
		if strings.EqualFold(x, s) {
			return true
		}
	}
	return false
}
