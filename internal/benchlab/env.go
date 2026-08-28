package benchlab

import (
	"bufio"
	"bytes"
	"os"
	"os/exec"
	"regexp"
	"runtime"
	"strconv"
	"strings"
)

// Environment captures the machine/tool-version context a benchmark result
// was produced under. Every field is best-effort: unknown values are left
// at zero rather than guessed.
type Environment struct {
	OS            string            `json:"os"`
	Arch          string            `json:"arch"`
	KernelVersion string            `json:"kernel_version,omitempty"`
	CPUModel      string            `json:"cpu_model,omitempty"`
	LogicalCPUs   int               `json:"logical_cpus"`
	TotalRAMBytes uint64            `json:"total_ram_bytes,omitempty"`
	GoVersion     string            `json:"go_version,omitempty"`
	ToolVersions  map[string]string `json:"tool_versions"`
	GitCommit     string            `json:"git_commit,omitempty"`
}

var katanaVersionRe = regexp.MustCompile(`(?i)version:\s*(\S+)`)

// CaptureEnvironment gathers the machine/tool-version context for the
// current run. Linux-specific probes are skipped on other platforms.
func CaptureEnvironment() Environment {
	env := Environment{
		OS:          runtime.GOOS,
		Arch:        runtime.GOARCH,
		LogicalCPUs: runtime.NumCPU(),
	}
	env.KernelVersion = firstLine(runOut("uname", "-r"))
	env.GoVersion = firstLine(runOut("go", "version"))
	env.GitCommit = firstLine(runOut("git", "rev-parse", "HEAD"))

	if runtime.GOOS == "linux" {
		env.CPUModel = cpuModelFromProc()
		env.TotalRAMBytes = totalRAMFromProc()
	}

	env.ToolVersions = map[string]string{
		"katana":    katanaVersion(),
		"hakrawler": hakrawlerVersion(),
		"gospider":  firstLine(strings.TrimPrefix(runOut("gospider", "--version"), "Version: ")),
	}
	return env
}

func katanaVersion() string {
	out := runOut("katana", "-version")
	if m := katanaVersionRe.FindStringSubmatch(out); m != nil {
		return stripANSI(m[1])
	}
	return ""
}

// hakrawler has no -version flag; fall back to the dpkg package version.
func hakrawlerVersion() string {
	out := runOut("dpkg-query", "-W", "-f=${Version}", "hakrawler")
	return firstLine(out)
}

var ansiRe = regexp.MustCompile(`\x1b\[[0-9;]*m`)

func stripANSI(s string) string { return ansiRe.ReplaceAllString(s, "") }

func runOut(name string, args ...string) string {
	cmd := exec.Command(name, args...)
	var buf bytes.Buffer
	cmd.Stdout = &buf
	cmd.Stderr = &buf // some tools print version info to stderr
	_ = cmd.Run()
	return buf.String()
}

func firstLine(s string) string {
	s = strings.TrimSpace(s)
	if i := strings.IndexByte(s, '\n'); i != -1 {
		return strings.TrimSpace(s[:i])
	}
	return s
}

func cpuModelFromProc() string {
	f, err := os.Open("/proc/cpuinfo")
	if err != nil {
		return ""
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			if i := strings.IndexByte(line, ':'); i != -1 {
				return strings.TrimSpace(line[i+1:])
			}
		}
	}
	return ""
}

func totalRAMFromProc() uint64 {
	f, err := os.Open("/proc/meminfo")
	if err != nil {
		return 0
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if !strings.HasPrefix(line, "MemTotal:") {
			continue
		}
		fields := strings.Fields(line)
		if len(fields) < 2 {
			return 0
		}
		kb, err := strconv.ParseUint(fields[1], 10, 64)
		if err != nil {
			return 0
		}
		return kb * 1024
	}
	return 0
}
