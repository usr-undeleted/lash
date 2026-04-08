package main

import (
	"bufio"
	"fmt"
	"os"
	"os/exec"
	"runtime"
	"strconv"
	"strings"
	"syscall"
)

type fetchField struct {
	short string
	long  string
	label string
}

type fetchCategory struct {
	fields []fetchField
	fetch  func() map[string]string
}

type fetchFormatIssue struct {
	category  string
	rawFormat string
	spec      string
}

type fetchSpec struct {
	category string
	format   string
}

var fetchCategoryMap = map[string]fetchCategory{
	"kernel": {
		fields: []fetchField{
			{"t", "type", "type"},
			{"v", "version", "version"},
			{"a", "arch", "architecture"},
			{"h", "hostname", "hostname"},
		},
		fetch: fetchKernel,
	},
	"os": {
		fields: []fetchField{
			{"n", "name", "name"},
			{"v", "version", "version"},
			{"c", "codename", "codename"},
			{"i", "icon", "icon"},
			{"k", "kernel", "kernel"},
		},
		fetch: fetchOS,
	},
	"shell": {
		fields: []fetchField{
			{"n", "name", "name"},
			{"v", "version", "version"},
		},
		fetch: fetchShell,
	},
	"user": {
		fields: []fetchField{
			{"u", "username", "username"},
			{"h", "hostname", "hostname"},
			{"d", "home", "home"},
			{"i", "uid", "uid"},
		},
		fetch: fetchUser,
	},
	"uptime": {
		fields: []fetchField{
			{"u", "uptime", "uptime"},
		},
		fetch: fetchUptime,
	},
	"memory": {
		fields: []fetchField{
			{"t", "total", "total"},
			{"u", "used", "used"},
			{"f", "free", "free"},
			{"a", "available", "available"},
		},
		fetch: fetchMemory,
	},
	"cpu": {
		fields: []fetchField{
			{"m", "model", "model"},
			{"c", "cores", "cores"},
			{"t", "threads", "threads"},
		},
		fetch: fetchCPU,
	},
	"gpu": {
		fields: []fetchField{
			{"m", "model", "model"},
			{"d", "driver", "driver"},
		},
		fetch: fetchGPU,
	},
	"desktop": {
		fields: []fetchField{
			{"e", "de", "desktop environment"},
			{"w", "wm", "window manager"},
			{"t", "terminal", "terminal"},
		},
		fetch: fetchDesktop,
	},
}

func builtinFetch(args []string, ctx *ExecContext) {
	if len(args) == 1 || args[1] == "--help" || args[1] == "-h" {
		printFetchHelp()
		lastExitCode = 0
		return
	}

	raw := false
	var specs []fetchSpec
	var allIssues []fetchFormatIssue

	i := 1
	for i < len(args) {
		if args[i] == "--raw" || args[i] == "-r" {
			raw = true
			i++
			continue
		}
		if args[i] == "{" || args[i] == "}" {
			i++
			continue
		}
		if args[i] == "|" || args[i] == ">>" || args[i] == ">" || args[i] == "<" || args[i] == "&" {
			break
		}
		cat := args[i]
		i++
		format := ""
		if i < len(args) && args[i] == "{" {
			i++
			var fmtParts []string
			for i < len(args) && args[i] != "}" {
				fmtParts = append(fmtParts, args[i])
				i++
			}
			format = strings.Join(fmtParts, "")
			if i < len(args) && args[i] == "}" {
				i++
			}
		}
		specs = append(specs, fetchSpec{cat, format})
	}

	if len(specs) == 0 {
		printFetchHelp()
		lastExitCode = 1
		return
	}

	for _, spec := range specs {
		cat, ok := fetchCategoryMap[spec.category]
		if !ok {
			fmt.Fprintf(os.Stderr, "fetch: unknown category: %s\n", spec.category)
			lastExitCode = 1
			return
		}
		if spec.format != "" {
			issues := validateFetchFormat(spec.category, spec.format, cat.fields)
			allIssues = append(allIssues, issues...)
		}
	}

	if len(allIssues) > 0 {
		printFetchFormatErrors(allIssues)
		lastExitCode = 1
		return
	}

	if raw {
		var allVals []string
		for _, spec := range specs {
			cat := fetchCategoryMap[spec.category]
			data := cat.fetch()
			fields := resolveFields(spec, cat.fields)
			for _, f := range fields {
				allVals = append(allVals, data[f.short])
			}
		}
		fmt.Println(strings.Join(allVals, " "))
		lastExitCode = 0
		return
	}

	first := true
	for _, spec := range specs {
		cat := fetchCategoryMap[spec.category]
		data := cat.fetch()
		fields := resolveFields(spec, cat.fields)
		if !first {
			fmt.Println()
		}
		first = false
		maxLen := 0
		for _, f := range fields {
			if len(f.label) > maxLen {
				maxLen = len(f.label)
			}
		}
		for _, f := range fields {
			val := data[f.short]
			fmt.Printf("  %-*s %s\n", maxLen, f.label, val)
		}
	}

	lastExitCode = 0
}

func resolveFields(spec fetchSpec, fields []fetchField) []fetchField {
	if spec.format == "" {
		return fields
	}
	seen := make(map[string]bool)
	var result []fetchField
	parts := strings.Split(spec.format, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		for _, f := range fields {
			if (f.short == p || f.long == p) && !seen[f.short] {
				result = append(result, f)
				seen[f.short] = true
				break
			}
		}
	}
	return result
}

func validateFetchFormat(category, format string, fields []fetchField) []fetchFormatIssue {
	var issues []fetchFormatIssue
	parts := strings.Split(format, ",")
	for _, p := range parts {
		p = strings.TrimSpace(p)
		if p == "" {
			continue
		}
		valid := false
		for _, f := range fields {
			if f.short == p || f.long == p {
				valid = true
				break
			}
		}
		if !valid {
			issues = append(issues, fetchFormatIssue{
				category:  category,
				rawFormat: format,
				spec:      p,
			})
		}
	}
	return issues
}

func printFetchFormatErrors(issues []fetchFormatIssue) {
	red := "\033[31m"
	bold := "\033[1m"
	reset := "\033[0m"
	for _, issue := range issues {
		fmt.Fprintf(os.Stderr, "%sfetch:%s invalid format for %s%s%s:\n", bold, reset, bold, issue.category, reset)
		parts := strings.Split(issue.rawFormat, ",")
		var colored []string
		for _, p := range parts {
			p = strings.TrimSpace(p)
			if p == issue.spec {
				colored = append(colored, red+p+reset)
			} else {
				colored = append(colored, p)
			}
		}
		fmt.Fprintf(os.Stderr, "  {%s}\n", strings.Join(colored, ", "))
		suggestion := suggestFix(issue.category, issue.spec)
		if suggestion != "" {
			fmt.Fprintf(os.Stderr, "  did you mean: %s%s%s?\n", bold, suggestion, reset)
		}
		fmt.Fprintln(os.Stderr)
	}
}

func suggestFix(category, spec string) string {
	cat, ok := fetchCategoryMap[category]
	if !ok {
		return ""
	}
	best := ""
	bestDist := len(spec) + 1
	for _, f := range cat.fields {
		d := levenshtein(spec, f.short)
		if d < bestDist {
			bestDist = d
			best = f.short
		}
		d = levenshtein(spec, f.long)
		if d < bestDist {
			bestDist = d
			best = f.long
		}
	}
	if bestDist <= 2 && best != "" {
		return best
	}
	return ""
}

func levenshtein(a, b string) int {
	la := len(a)
	lb := len(b)
	if la == 0 {
		return lb
	}
	if lb == 0 {
		return la
	}
	matrix := make([][]int, la+1)
	for i := range matrix {
		matrix[i] = make([]int, lb+1)
		matrix[i][0] = i
	}
	for j := 0; j <= lb; j++ {
		matrix[0][j] = j
	}
	for i := 1; i <= la; i++ {
		for j := 1; j <= lb; j++ {
			cost := 1
			if a[i-1] == b[j-1] {
				cost = 0
			}
			matrix[i][j] = min3(
				matrix[i-1][j]+1,
				matrix[i][j-1]+1,
				matrix[i-1][j-1]+cost,
			)
		}
	}
	return matrix[la][lb]
}

func min3(a, b, c int) int {
	if a < b {
		if a < c {
			return a
		}
		return c
	}
	if b < c {
		return b
	}
	return c
}

func printFetchHelp() {
	help := `fetch — display system information

usage: fetch [--raw|-r] [category{format} ...]
       fetch -h|--help

flags:
  --raw, -r          raw output (space-separated values, no labels)

categories:
  kernel{t,v,a,h}   type, version, architecture, hostname
  os{n,v,c,i,k}     distro name, version, codename, icon, kernel
  shell{n,v}        shell name, version
  user{u,h,d,i}     username, hostname, home directory, uid
  uptime{u}         uptime
  memory{t,u,f,a}   total, used, free, available
  cpu{m,c,t}        model, cores, threads
  gpu{m,d}          model, driver
  desktop{e,w,t}    desktop environment, window manager, terminal

format options:
  {} or omitted     show all fields for the category
  {t,v,a}           show only the specified fields (single letter or full word)
  {type,version}    full word aliases accepted for each letter

examples:
  fetch                     show all categories with all fields
  fetch kernel              show all kernel fields
  fetch kernel{t,v}         show kernel type and version only
  fetch os{icon}            show distro icon only
  fetch kernel{t,v} user{u}
  fetch --raw kernel{t,v}   raw: "Linux 6.19.10-1-cachyos"`
	fmt.Println(help)
}

func fetchKernel() map[string]string {
	result := make(map[string]string)
	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err == nil {
		result["t"] = int8ToStr(uname.Sysname[:])
		result["v"] = int8ToStr(uname.Release[:])
		result["a"] = int8ToStr(uname.Machine[:])
	}
	if h, err := os.Hostname(); err == nil {
		result["h"] = h
	}
	for _, k := range []string{"t", "v", "a", "h"} {
		if result[k] == "" {
			result[k] = "unknown"
		}
	}
	return result
}

func fetchOS() map[string]string {
	result := make(map[string]string)
	osRel := parseOSRelease()

	if name, ok := osRel["PRETTY_NAME"]; ok && name != "" {
		result["n"] = name
	} else if name, ok := osRel["NAME"]; ok && name != "" {
		result["n"] = name
	} else {
		id := getDistroID()
		if id != "" {
			result["n"] = strings.Title(id)
		} else {
			result["n"] = "unknown"
		}
	}

	if v, ok := osRel["VERSION"]; ok && v != "" {
		result["v"] = v
	} else if v, ok := osRel["VERSION_ID"]; ok && v != "" {
		result["v"] = v
	} else {
		result["v"] = "unknown"
	}

	if c, ok := osRel["VERSION_CODENAME"]; ok && c != "" {
		result["c"] = c
	} else {
		result["c"] = "unknown"
	}

	result["i"] = getOSIcon()
	if result["i"] == "?" {
		result["i"] = "unknown"
	}

	var uname syscall.Utsname
	if err := syscall.Uname(&uname); err == nil {
		result["k"] = int8ToStr(uname.Release[:])
	} else {
		result["k"] = "unknown"
	}

	return result
}

func fetchShell() map[string]string {
	result := make(map[string]string)
	result["n"] = "lash"
	result["v"] = getVersion()
	return result
}

func fetchUser() map[string]string {
	result := make(map[string]string)
	result["u"] = os.Getenv("USER")
	if result["u"] == "" {
		result["u"] = "unknown"
	}
	if h, err := os.Hostname(); err == nil {
		result["h"] = h
	} else {
		result["h"] = "unknown"
	}
	result["d"] = os.Getenv("HOME")
	if result["d"] == "" {
		if dir, err := os.UserHomeDir(); err == nil {
			result["d"] = dir
		} else {
			result["d"] = "unknown"
		}
	}
	result["i"] = strconv.Itoa(os.Getuid())
	return result
}

func fetchUptime() map[string]string {
	result := make(map[string]string)
	if runtime.GOOS != "linux" {
		result["u"] = "unknown"
		return result
	}
	data, err := os.ReadFile("/proc/uptime")
	if err != nil {
		result["u"] = "unknown"
		return result
	}
	fields := strings.Fields(string(data))
	if len(fields) < 1 {
		result["u"] = "unknown"
		return result
	}
	secs, err := strconv.ParseFloat(fields[0], 64)
	if err != nil {
		result["u"] = "unknown"
		return result
	}
	result["u"] = formatUptime(secs)
	return result
}

func fetchMemory() map[string]string {
	result := make(map[string]string)
	for _, k := range []string{"t", "u", "f", "a"} {
		result[k] = "unknown"
	}
	if runtime.GOOS != "linux" {
		return result
	}
	data, err := os.ReadFile("/proc/meminfo")
	if err != nil {
		return result
	}
	meminfo := parseMeminfo(string(data))
	total := meminfo["MemTotal"]
	free := meminfo["MemFree"]
	avail := meminfo["MemAvailable"]
	buf := meminfo["Buffers"]
	cached := meminfo["Cached"]

	if avail == 0 {
		avail = free + buf + cached
	}
	used := total - avail

	if total > 0 {
		result["t"] = formatBytes(total * 1024)
		result["u"] = formatBytes(used * 1024)
		result["f"] = formatBytes(free * 1024)
		result["a"] = formatBytes(avail * 1024)
	}
	return result
}

func fetchCPU() map[string]string {
	result := make(map[string]string)
	result["m"] = "unknown"
	result["c"] = "unknown"
	result["t"] = "unknown"
	if runtime.GOOS != "linux" {
		return result
	}
	data, err := os.ReadFile("/proc/cpuinfo")
	if err != nil {
		return result
	}
	modelName := ""
	threadCount := 0
	physicalCores := make(map[string]bool)
	scanner := bufio.NewScanner(strings.NewReader(string(data)))
	var curPhysical, curCore string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "model name") {
			if idx := strings.Index(line, ":"); idx > 0 {
				modelName = strings.TrimSpace(line[idx+1:])
			}
		}
		if strings.HasPrefix(line, "processor") {
			threadCount++
		}
		if strings.HasPrefix(line, "physical id") {
			if idx := strings.Index(line, ":"); idx > 0 {
				curPhysical = strings.TrimSpace(line[idx+1:])
			}
		}
		if strings.HasPrefix(line, "core id") {
			if idx := strings.Index(line, ":"); idx > 0 {
				curCore = strings.TrimSpace(line[idx+1:])
				physicalCores[curPhysical+":"+curCore] = true
			}
		}
	}
	if modelName != "" {
		result["m"] = modelName
	}
	if len(physicalCores) > 0 {
		result["c"] = strconv.Itoa(len(physicalCores))
	}
	if threadCount > 0 {
		result["t"] = strconv.Itoa(threadCount)
	}
	return result
}

func fetchGPU() map[string]string {
	result := map[string]string{"m": "unknown", "d": "unknown"}
	cmd := exec.Command("lspci", "-k")
	output, err := cmd.Output()
	if err != nil {
		return result
	}
	scanner := bufio.NewScanner(strings.NewReader(string(output)))
	inGPU := false
	for scanner.Scan() {
		line := scanner.Text()
		if strings.Contains(line, "VGA") || strings.Contains(line, "3D controller") || strings.Contains(line, "Display controller") {
			inGPU = true
			if idx := strings.Index(line, ":"); idx > 0 {
				model := strings.TrimSpace(line[idx+1:])
				result["m"] = model
			}
			continue
		}
		if inGPU {
			if strings.Contains(line, "Kernel driver in use:") {
				if idx := strings.Index(line, ":"); idx > 0 {
					driver := strings.TrimSpace(line[idx+1:])
					if driver != "" {
						result["d"] = driver
					}
				}
				break
			}
			if !strings.HasPrefix(line, "\t") && !strings.HasPrefix(line, " ") {
				break
			}
		}
	}
	return result
}

func fetchDesktop() map[string]string {
	result := make(map[string]string)
	de := os.Getenv("XDG_CURRENT_DESKTOP")
	if de == "" {
		de = os.Getenv("DESKTOP_SESSION")
	}
	if de == "" {
		de = "unknown"
	}
	result["e"] = de

	wm := os.Getenv("XDG_SESSION_TYPE")
	if wm == "" {
		if os.Getenv("WAYLAND_DISPLAY") != "" {
			wm = "wayland"
		} else if os.Getenv("DISPLAY") != "" {
			wm = "x11"
		} else {
			wm = "unknown"
		}
	}
	result["w"] = wm

	term := os.Getenv("TERM_PROGRAM")
	if term == "" {
		term = os.Getenv("TERM")
	}
	if term == "" {
		term = "unknown"
	}
	result["t"] = term

	return result
}

func int8ToStr(arr []int8) string {
	var buf []byte
	for _, b := range arr {
		if b == 0 {
			break
		}
		buf = append(buf, byte(b))
	}
	return string(buf)
}

func parseOSRelease() map[string]string {
	result := make(map[string]string)
	f, err := os.Open("/etc/os-release")
	if err != nil {
		return result
	}
	defer f.Close()
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if eq := strings.Index(line, "="); eq > 0 {
			key := line[:eq]
			val := strings.Trim(line[eq+1:], "\"'")
			result[key] = val
		}
	}
	return result
}

func formatUptime(secs float64) string {
	days := int(secs) / 86400
	hours := int(secs) % 86400 / 3600
	mins := int(secs) % 3600 / 60
	var parts []string
	if days > 0 {
		parts = append(parts, fmt.Sprintf("%dd", days))
	}
	if hours > 0 {
		parts = append(parts, fmt.Sprintf("%dh", hours))
	}
	if mins > 0 {
		parts = append(parts, fmt.Sprintf("%dm", mins))
	}
	if len(parts) == 0 {
		parts = append(parts, fmt.Sprintf("%ds", int(secs)))
	}
	return strings.Join(parts, " ")
}

func parseMeminfo(data string) map[string]uint64 {
	result := make(map[string]uint64)
	scanner := bufio.NewScanner(strings.NewReader(data))
	for scanner.Scan() {
		line := scanner.Text()
		if idx := strings.Index(line, ":"); idx > 0 {
			key := strings.TrimSpace(line[:idx])
			val := strings.TrimSpace(line[idx+1:])
			val = strings.TrimSuffix(val, " kB")
			if n, err := strconv.ParseUint(val, 10, 64); err == nil {
				result[key] = n
			}
		}
	}
	return result
}

func formatBytes(b uint64) string {
	const (
		KB = 1024
		MB = KB * 1024
		GB = MB * 1024
		TB = GB * 1024
	)
	switch {
	case b >= TB:
		return fmt.Sprintf("%.1f TiB", float64(b)/float64(TB))
	case b >= GB:
		return fmt.Sprintf("%.1f GiB", float64(b)/float64(GB))
	case b >= MB:
		return fmt.Sprintf("%.1f MiB", float64(b)/float64(MB))
	case b >= KB:
		return fmt.Sprintf("%.1f KiB", float64(b)/float64(KB))
	default:
		return fmt.Sprintf("%d B", b)
	}
}
