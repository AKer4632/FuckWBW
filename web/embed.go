// Package web 提供嵌入的 HTML 资源（indexHTML 变量由 server.go 引用）。
package web

import _ "embed"

//go:embed index.html
var indexHTML []byte
