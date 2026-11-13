package api

import "net/http"

// Static 返回 web/ 目录的静态资源 handler（15 页高保真原型 + API 联调脚本）。
func Static(webDir string) http.Handler {
	return http.FileServer(http.Dir(webDir))
}
