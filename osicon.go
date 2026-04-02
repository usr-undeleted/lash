package main

import (
	"bufio"
	"os"
	"runtime"
	"strings"
)

var distroIcons = map[string]string{
	"ubuntu":              "\U000F0548",
	"debian":              "\U000F08DA",
	"fedora":              "\U000F08DB",
	"arch":                "\U000F08C7",
	"artix":               "\uF31F",
	"manjaro":             "\uF312",
	"linuxmint":           "\U000F08ED",
	"gentoo":              "\U000F08E8",
	"nixos":               "\U000F1105",
	"opensuse":            "\uEF6D",
	"opensuse-tumbleweed": "\uF37D",
	"opensuse-leap":       "\uF37E",
	"alpine":              "\uF300",
	"centos":              "\uE78A",
	"redhat":              "\uEF5D",
	"rhel":                "\uEF5D",
	"pop":                 "\uF32A",
	"pop-os":              "\uF32A",
	"void":                "\uF32E",
	"endeavouros":         "\uF322",
	"kali":                "\uF327",
	"sabayon":             "\uF317",
	"slackware":           "\uF318",
	"alma":                "\uF31D",
	"almalinux":           "\uF31D",
	"rocky":               "\uF32B",
	"rockeylinux":         "\uF32B",
	"solus":               "\uF32D",
	"zorin":               "\uF32F",
	"android":             "\uE70E",
}

const iconMacOS = "\uF179"
const iconWindows = "\uF372"
const iconLinux = "\uE712"

func getNamedIcon(name string) string {
	id := strings.ToLower(name)
	switch id {
	case "macos", "darwin":
		return iconMacOS
	case "windows", "win":
		return iconWindows
	case "linux", "tux":
		return iconLinux
	}
	if icon, ok := distroIcons[id]; ok {
		return icon
	}
	return ""
}

func getOSIcon() string {
	switch runtime.GOOS {
	case "darwin":
		return iconMacOS
	case "windows":
		return iconWindows
	}

	if isWSL() {
		distro := os.Getenv("WSL_DISTRO_NAME")
		if distro != "" {
			id := strings.ToLower(distro)
			if icon, ok := distroIcons[id]; ok {
				return icon
			}
		}
		return iconWindows
	}

	if isAndroid() {
		return distroIcons["android"]
	}

	id := getDistroID()
	if id == "" {
		return "?"
	}
	if icon, ok := distroIcons[id]; ok {
		return icon
	}
	return iconLinux
}

func isAndroid() bool {
	return os.Getenv("ANDROID_ROOT") != ""
}

func isWSL() bool {
	if os.Getenv("WSL_DISTRO_NAME") != "" {
		return true
	}
	data, err := os.ReadFile("/proc/version")
	if err != nil {
		return false
	}
	return strings.Contains(strings.ToLower(string(data)), "microsoft")
}

func getDistroID() string {
	if isAndroid() {
		return "android"
	}

	f, err := os.Open("/etc/os-release")
	if err != nil {
		return ""
	}
	defer f.Close()

	var id string
	scanner := bufio.NewScanner(f)
	for scanner.Scan() {
		line := scanner.Text()
		if strings.HasPrefix(line, "ID=") {
			id = strings.Trim(line[3:], "\"'")
			id = strings.ToLower(id)
		}
		if strings.HasPrefix(line, "ID_LIKE=") && id == "" {
			id = strings.Trim(line[8:], "\"'")
			id = strings.ToLower(id)
		}
	}

	return id
}
