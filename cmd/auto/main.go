// 无 UI 批跑入口：读环境变量 ACCOUNTS / STEPS，逐账号自动登录取设备并刷步。
//
// ACCOUNTS 支持两种格式（二选一）：
//  1) 多行 account:password
//       13369219993:219993
//       13800138000:pass2
//  2) JSON 数组
//       [{"account":"133...","password":"xxx"},{"user":"...","pass":"..."}]
//
// STEPS:
//   - 空 / random / rand → 每账号在 10000~13000 间随机
//   - 数字 → 固定步数
// 也可设 STEP_NUMBER；RANDOM_STEPS=1 强制随机。
package main

import (
	"context"
	"encoding/json"
	"fmt"
	"math/rand"
	"os"
	"strconv"
	"strings"
	"time"

	"fkw/pipeline"
)

const (
	randomStepsMin = 10000
	randomStepsMax = 13000
)

type account struct {
	Account  string
	Password string
}

func main() {
	accounts, err := loadAccounts(os.Getenv("ACCOUNTS"))
	if err != nil {
		fmt.Fprintf(os.Stderr, "[-] 解析 ACCOUNTS 失败: %v\n", err)
		os.Exit(2)
	}
	if len(accounts) == 0 {
		fmt.Fprintln(os.Stderr, "[-] ACCOUNTS 为空。请在 GitHub Secrets 中配置，格式见 README。")
		os.Exit(2)
	}

	rng := rand.New(rand.NewSource(time.Now().UnixNano()))
	stepsMode, fixedSteps := parseStepsMode(
		firstNonEmpty(os.Getenv("STEPS"), os.Getenv("STEP_NUMBER")),
		os.Getenv("RANDOM_STEPS"),
	)
	if stepsMode == "random" {
		fmt.Printf("[*] 账号数=%d 步数模式=随机(%d~%d)\n", len(accounts), randomStepsMin, randomStepsMax)
	} else {
		fmt.Printf("[*] 账号数=%d 步数模式=固定 %d\n", len(accounts), fixedSteps)
	}

	fail := 0
	for i, a := range accounts {
		mask := maskAccount(a.Account)
		steps := fixedSteps
		if stepsMode == "random" {
			steps = randomStepsMin + rng.Intn(randomStepsMax-randomStepsMin+1)
		}
		fmt.Printf("\n========== [%d/%d] %s steps=%d ==========\n", i+1, len(accounts), mask, steps)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Minute)
		r := pipeline.Run(ctx, func(line string) { fmt.Println(line) }, a.Account, a.Password, steps)
		cancel()
		if r.Ok {
			fmt.Printf("[+] %s 成功 steps=%d serial=%s userid=%d code=%s\n",
				mask, r.StepNumber, r.DeviceSerial, r.UserId, r.ResultCode)
		} else {
			fail++
			fmt.Printf("[-] %s 失败 code=%q err=%q body=%s\n",
				mask, r.ResultCode, r.Err, truncate(r.ResponseBody, 200))
		}
		// 账号间隔，避免触发风控
		if i+1 < len(accounts) {
			time.Sleep(5 * time.Second)
		}
	}

	fmt.Printf("\n=== 汇总: 成功 %d / 失败 %d / 共 %d ===\n",
		len(accounts)-fail, fail, len(accounts))
	if fail > 0 {
		os.Exit(1)
	}
}

// parseStepsMode 返回 mode=random|fixed 与固定步数。
func parseStepsMode(stepsEnv, randomEnv string) (mode string, fixed int) {
	randomEnv = strings.TrimSpace(strings.ToLower(randomEnv))
	if randomEnv == "1" || randomEnv == "true" || randomEnv == "yes" || randomEnv == "on" {
		return "random", 0
	}
	s := strings.TrimSpace(strings.ToLower(stepsEnv))
	if s == "" || s == "random" || s == "rand" || s == "rnd" {
		return "random", 0
	}
	if n, err := strconv.Atoi(s); err == nil && n >= 1000 {
		return "fixed", n
	}
	// 无法解析时走随机
	return "random", 0
}

func loadAccounts(raw string) ([]account, error) {
	raw = strings.TrimSpace(raw)
	if raw == "" {
		return nil, nil
	}
	// JSON 数组
	if strings.HasPrefix(raw, "[") {
		var arr []map[string]string
		if err := json.Unmarshal([]byte(raw), &arr); err != nil {
			return nil, err
		}
		out := make([]account, 0, len(arr))
		for _, m := range arr {
			acc := firstNonEmpty(m["account"], m["user"], m["username"], m["phone"])
			pass := firstNonEmpty(m["password"], m["pass"], m["pwd"])
			if acc == "" || pass == "" {
				continue
			}
			out = append(out, account{Account: acc, Password: pass})
		}
		return out, nil
	}
	// 多行 account:password 或 account,password 或 account password
	var out []account
	for _, line := range strings.Split(raw, "\n") {
		line = strings.TrimSpace(line)
		if line == "" || strings.HasPrefix(line, "#") {
			continue
		}
		acc, pass, ok := splitAccountLine(line)
		if !ok {
			return nil, fmt.Errorf("无法解析行: %q（需要 account:password）", line)
		}
		out = append(out, account{Account: acc, Password: pass})
	}
	return out, nil
}

func splitAccountLine(line string) (string, string, bool) {
	// 优先冒号；密码里可能有冒号，只切第一个
	if i := strings.Index(line, ":"); i > 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
	}
	if i := strings.Index(line, ","); i > 0 {
		return strings.TrimSpace(line[:i]), strings.TrimSpace(line[i+1:]), true
	}
	parts := strings.Fields(line)
	if len(parts) >= 2 {
		return parts[0], parts[1], true
	}
	return "", "", false
}

func firstNonEmpty(ss ...string) string {
	for _, s := range ss {
		if strings.TrimSpace(s) != "" {
			return strings.TrimSpace(s)
		}
	}
	return ""
}

func maskAccount(s string) string {
	if len(s) <= 4 {
		return "****"
	}
	if len(s) <= 7 {
		return s[:2] + "****"
	}
	return s[:3] + "****" + s[len(s)-2:]
}

func truncate(s string, n int) string {
	r := []rune(s)
	if len(r) <= n {
		return s
	}
	return string(r[:n]) + "..."
}
