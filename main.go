package main

import (
	"log"

	"fkw/web"
)

func main() {
	if err := web.ListenAndServe(":8080"); err != nil {
		log.Fatalf("web 启动失败: %v", err)
	}
}
