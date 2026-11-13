package api

import (
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
)

func webDir() string { return filepath.Join("..", "..", "web") }

func readWeb(t *testing.T, name string) string {
	t.Helper()
	data, err := os.ReadFile(filepath.Join(webDir(), name))
	if err != nil {
		t.Fatal(err)
	}
	return string(data)
}

func TestFrontend_HighFidelityPagesServed(t *testing.T) {
	handler := Static(webDir())
	for _, path := range []string{"/", "/app.js"} {
		req := httptest.NewRequest(http.MethodGet, path, nil)
		rec := httptest.NewRecorder()
		handler.ServeHTTP(rec, req)
		if rec.Code != http.StatusOK {
			t.Fatalf("%s 应可静态访问，实际 %d", path, rec.Code)
		}
	}
	// http.FileServer 对 /index.html 统一 301 到 ./，资源仍可经根路径访问。
	redirect := httptest.NewRecorder()
	handler.ServeHTTP(redirect, httptest.NewRequest(http.MethodGet, "/index.html", nil))
	if redirect.Code != http.StatusMovedPermanently || redirect.Header().Get("Location") != "./" {
		t.Fatalf("/index.html 应重定向到根，实际 %d %s",
			redirect.Code, redirect.Header().Get("Location"))
	}
	html := readWeb(t, "index.html")
	if !strings.Contains(html, "毛球驿站") {
		t.Fatal("应以新高保真原型为视觉基准")
	}
	pages := []string{"U1", "U2", "U3", "U4", "F1", "F2", "F3", "C1", "C2", "C3",
		"M1", "M2", "M3", "M4", "P1"}
	for _, page := range pages {
		if !strings.Contains(html, page) {
			t.Fatalf("原型缺少页面编号 %s", page)
		}
	}
	if !strings.Contains(html, `src="/app.js"`) {
		t.Fatal("原型必须引入 API 联调脚本")
	}
}

func TestFrontend_AppScriptContract(t *testing.T) {
	js := readWeb(t, "app.js")
	for _, want := range []string{
		"requireRole", "formatYuan", "/api/slots", "/api/appointments",
		"/api/settlement", "/api/channel/refund-outcome", "/api/holds/expire",
		"/api/refunds/retry", "/api/cancel-requests", "X-Role", "X-User-Id", "X-Store-Id",
		"toast", "textContent",
	} {
		if !strings.Contains(js, want) {
			t.Fatalf("app.js 缺少联调契约: %s", want)
		}
	}
	if strings.Contains(js, "innerHTML") {
		t.Fatal("禁止用 innerHTML 拼接用户可控内容（XSS）")
	}
}

func TestFrontend_MoneyFormattingOnly(t *testing.T) {
	js := readWeb(t, "app.js")
	if !strings.Contains(js, "toFixed(2)") {
		t.Fatal("金额展示必须做分→元格式化")
	}
	if strings.Contains(js, "amount_fen") && strings.Contains(js, "amount_fen *") {
		t.Fatal("金额不得在前端参与算术")
	}
}
