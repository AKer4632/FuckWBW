package main

import (
	"flag"
	"fmt"
	"log"
	"time"
)

func main() {
	days := flag.Int("d", 0, "日期偏移: 0=今天, 1=昨天, 2=前天")
	flag.Parse()

	if *days < 0 || *days > 2 {
		log.Fatal("-d 只支持 0/1/2")
	}

	for i := 0; i <= *days; i++ {
		target := time.Now().AddDate(0, 0, -i)
		dateStr := target.Format("2006-01-02")

		fmt.Printf("[%d] %s done\n", i, dateStr)
	}
}
