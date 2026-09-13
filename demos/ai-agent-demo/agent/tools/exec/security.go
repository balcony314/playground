package exec

// ═══════════════════════════════════════════════════════════════
// security.go — 命令安全防护模块
// ═══════════════════════════════════════════════════════════════
//
// 三层防护机制：
//   1. 命令黑名单：阻止已知的危险命令
//   2. 路径访问控制：限制访问系统关键路径
//   3. 敏感操作检测：需要用户确认的操作
//
// 设计原则：安全优先，宁可误拦不可漏放
//
// 注意：黑名单是尽力而为的防护，不能完全阻止所有危险命令。
// Shell 变量展开、反斜杠转义、引号拆分等方式可能绕过检测。
// 生产环境应使用沙箱（如容器、seccomp）进行隔离。

import (
	"regexp"
	"strings"
)

// ValidationResult 安全验证结果
type ValidationResult struct {
	Allowed        bool   // 是否允许执行
	Reason         string // 拒绝原因（Allowed=false 时）
	NeedsConfirm   bool   // 是否需要用户确认
	ConfirmMessage string // 确认提示信息（NeedsConfirm=true 时）
}

// SecurityChecker 命令安全检查器
type SecurityChecker struct {
	allowedDir string // 允许访问的根目录（项目目录）
}

// NewSecurityChecker 创建安全检查器
// allowedDir 为允许访问的目录，空字符串表示不限制
func NewSecurityChecker(allowedDir string) *SecurityChecker {
	return &SecurityChecker{
		allowedDir: allowedDir,
	}
}

// Validate 验证命令安全性
func (sc *SecurityChecker) Validate(command string) ValidationResult {
	command = strings.TrimSpace(command)
	if command == "" {
		return ValidationResult{Allowed: false, Reason: "命令不能为空"}
	}

	// 规范化命令（合并多余空格）
	normalizedCmd := normalizeCommand(command)

	// 第一层：命令黑名单检查
	if result := sc.checkBlacklist(normalizedCmd); !result.Allowed {
		return result
	}

	// 第二层：路径访问控制
	if result := sc.checkPathAccess(command); !result.Allowed {
		return result
	}

	// 第三层：敏感操作检测
	if result := sc.checkSensitiveOps(command); result.NeedsConfirm {
		return result
	}

	return ValidationResult{Allowed: true}
}

// multiSpaceRe 匹配连续空白字符，用于规范化命令
var multiSpaceRe = regexp.MustCompile(`\s+`)

// normalizeCommand 规范化命令字符串：合并多余空格
func normalizeCommand(command string) string {
	return strings.TrimSpace(multiSpaceRe.ReplaceAllString(command, " "))
}

// ─── 命令黑名单 ─────────────────────────────────────────────

// blockedPrefixes 被阻止的命令前缀（规范化后匹配）
var blockedPrefixes = []struct {
	prefix string
	reason string
}{
	// 危险的删除操作
	{"rm -rf /", "禁止递归删除根目录"},
	{"rm -rf ~", "禁止递归删除用户目录"},
	{"rm -rf /*", "禁止递归删除根目录"},
	{"rm -vrf /", "禁止递归删除根目录"},
	{"rm -fr /", "禁止递归删除根目录"},

	// 磁盘操作
	{"dd if=", "禁止直接磁盘操作"},
	{"mkfs", "禁止格式化文件系统"},
	{"fdisk", "禁止磁盘分区操作"},

	// 系统控制
	{"shutdown", "禁止关机操作"},
	{"reboot", "禁止重启操作"},
	{"halt", "禁止停机操作"},
	{"poweroff", "禁止关机操作"},

	// Fork 炸弹
	{":(){", "禁止 fork 炸弹"},

	// 危险的权限操作
	{"chmod -r 777 /", "禁止递归设置根目录权限为 777"},
	{"chmod 777 /", "禁止设置根目录权限为 777"},
	{"chown -r", "禁止递归修改所有者"},

	// 危险的 sudo 操作
	{"sudo ", "禁止使用 sudo"},

	// 危险的进程操作
	{"kill -9 1", "禁止杀死 init 进程"},

	// 代码执行
	{"eval ", "禁止使用 eval 执行代码"},
	{"bash -c", "禁止使用 bash -c 执行代码"},
	{"sh -c", "禁止使用 sh -c 嵌套执行"},
	{"source ", "禁止使用 source 执行脚本"},
	{". ", "禁止使用 source 执行脚本"},
}

// blockedPatterns 被阻止的命令正则模式
var blockedPatterns = []struct {
	pattern *regexp.Regexp
	reason  string
}{
	// Pipe to shell 攻击（支持无空格的管道）
	{regexp.MustCompile(`curl\s+.*\|\s*(ba)?sh`), "禁止 curl 管道执行"},
	{regexp.MustCompile(`wget\s+.*\|\s*(ba)?sh`), "禁止 wget 管道执行"},

	// 写入设备文件
	{regexp.MustCompile(`>\s*/dev/`), "禁止写入设备文件"},

	// 危险的重定向
	{regexp.MustCompile(`\|\s*sudo`), "禁止管道到 sudo"},

	// 命令替换（反引号和 $()）
	{regexp.MustCompile("`[^`]+`"), "禁止使用命令替换"},
	{regexp.MustCompile(`\$\([^)]+\)`), "禁止使用命令替换"},

	// 命令链（分号、&&、||）
	{regexp.MustCompile(`;\s*rm\s`), "禁止命令链中的危险操作"},
	{regexp.MustCompile(`&&\s*rm\s`), "禁止命令链中的危险操作"},
	{regexp.MustCompile(`\|\|\s*rm\s`), "禁止命令链中的危险操作"},

	// 环境变量注入
	{regexp.MustCompile(`LD_PRELOAD\s*=`), "禁止 LD_PRELOAD 注入"},
	{regexp.MustCompile(`LD_LIBRARY_PATH\s*=`), "禁止 LD_LIBRARY_PATH 注入"},

	// 危险的 shellshock 模式
	{regexp.MustCompile(`\(\)\s*\{`), "禁止 shellshock 模式"},
}

// checkBlacklist 检查命令是否在黑名单中
func (sc *SecurityChecker) checkBlacklist(command string) ValidationResult {
	lowerCmd := strings.ToLower(command)

	// 检查前缀
	for _, blocked := range blockedPrefixes {
		if strings.HasPrefix(lowerCmd, blocked.prefix) {
			return ValidationResult{
				Allowed: false,
				Reason:  blocked.reason,
			}
		}
	}

	// 检查正则模式
	for _, blocked := range blockedPatterns {
		if blocked.pattern.MatchString(command) {
			return ValidationResult{
				Allowed: false,
				Reason:  blocked.reason,
			}
		}
	}

	return ValidationResult{Allowed: true}
}

// ─── 路径访问控制 ───────────────────────────────────────────

// restrictedPaths 受限制的系统路径
var restrictedPaths = []string{
	"/etc",
	"/usr",
	"/bin",
	"/sbin",
	"/boot",
	"/dev",
	"/proc",
	"/sys",
	"/root",
	"/home",
	"/tmp",
	"/var",
	"/opt",
}

// pathPattern 匹配路径的正则表达式
var pathPattern = regexp.MustCompile(`(?:^|\s)(/[a-zA-Z0-9_./-]+)`)

// checkPathAccess 检查命令是否访问受限路径
func (sc *SecurityChecker) checkPathAccess(command string) ValidationResult {
	// 如果没有设置允许目录，不进行路径检查
	if sc.allowedDir == "" {
		return ValidationResult{Allowed: true}
	}

	matches := pathPattern.FindAllStringSubmatch(command, -1)
	for _, match := range matches {
		if len(match) < 2 {
			continue
		}
		path := match[1]

		// 检查是否访问受限路径
		for _, restricted := range restrictedPaths {
			if strings.HasPrefix(path, restricted) {
				return ValidationResult{
					Allowed: false,
					Reason:  "禁止访问系统路径: " + restricted,
				}
			}
		}
	}

	return ValidationResult{Allowed: true}
}

// ─── 敏感操作检测 ───────────────────────────────────────────

// sensitivePatterns 需要确认的敏感操作
var sensitivePatterns = []struct {
	pattern *regexp.Regexp
	message string
}{
	// Git 操作
	{regexp.MustCompile(`git\s+push`), "git push 会推送代码到远程仓库"},
	{regexp.MustCompile(`git\s+reset\s+--hard`), "git reset --hard 会丢失未提交的修改"},
	{regexp.MustCompile(`git\s+clean\s+-[a-z]*f`), "git clean -f 会删除未跟踪的文件"},
	{regexp.MustCompile(`git\s+checkout\s+\.\s*$`), "git checkout . 会丢弃所有未提交的修改"},

	// 文件删除
	{regexp.MustCompile(`(?:^|\s)rm\s+`), "rm 命令会删除文件"},

	// 权限修改
	{regexp.MustCompile(`chmod\s+`), "chmod 会修改文件权限"},
	{regexp.MustCompile(`chown\s+`), "chown 会修改文件所有者"},

	// Docker 操作
	{regexp.MustCompile(`docker\s+rm\s+`), "docker rm 会删除容器"},
	{regexp.MustCompile(`docker\s+kill\s+`), "docker kill 会强制停止容器"},
	{regexp.MustCompile(`docker\s+compose\s+down`), "docker compose down 会停止并删除容器"},

	// 包管理发布（只标记发布，不标记安装）
	{regexp.MustCompile(`npm\s+publish`), "npm publish 会发布包到 npm"},
}

// checkSensitiveOps 检查是否为敏感操作
func (sc *SecurityChecker) checkSensitiveOps(command string) ValidationResult {
	for _, sensitive := range sensitivePatterns {
		if sensitive.pattern.MatchString(command) {
			return ValidationResult{
				Allowed:        true,
				NeedsConfirm:   true,
				ConfirmMessage: "检测到敏感操作: " + sensitive.message + "。请确认是否继续执行。",
			}
		}
	}

	return ValidationResult{Allowed: true}
}
