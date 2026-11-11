package hub

import (
	"debug/elf"
	"debug/macho"
	"net/http"
	"os"
	"path/filepath"
	"runtime"

	"github.com/tencent/codex-bridge/internal/serverutil"
)

func serveBridgeBinary(w http.ResponseWriter, r *http.Request, executable, arch string) {
	serveBridgePlatformBinary(w, r, executable, "linux", arch)
}

func serveBridgePlatformBinary(w http.ResponseWriter, r *http.Request, executable, platform, arch string) {
	w.Header().Set("Cache-Control", "no-store")
	if platform != "linux" && platform != "darwin" {
		serverutil.WriteError(w, http.StatusNotFound, "UNSUPPORTED_PLATFORM", "unsupported Bridge operating system")
		return
	}
	machine := elf.EM_X86_64
	switch arch {
	case "amd64":
	case "arm64":
		machine = elf.EM_AARCH64
	default:
		serverutil.WriteError(w, http.StatusNotFound, "UNSUPPORTED_PLATFORM", "unsupported Bridge architecture")
		return
	}
	name := "codex-bridge-" + platform + "-" + arch
	path := executable
	if runtime.GOOS != platform || runtime.GOARCH != arch {
		path = filepath.Join(filepath.Dir(executable), "downloads", name)
	}
	f, err := os.Open(path)
	if err != nil {
		serverutil.WriteError(w, http.StatusNotFound, "BINARY_NOT_FOUND", "Bridge binary is not available for "+platform+"/"+arch)
		return
	}
	defer f.Close()
	info, err := f.Stat()
	if err != nil || !info.Mode().IsRegular() {
		serverutil.WriteError(w, http.StatusInternalServerError, "INVALID_BINARY", "Bridge artifact is not a regular file")
		return
	}
	valid := false
	if platform == "darwin" {
		cpu := macho.CpuAmd64
		if arch == "arm64" {
			cpu = macho.CpuArm64
		}
		artifact, parseErr := macho.NewFile(f)
		valid = parseErr == nil && artifact.Magic == macho.Magic64 && artifact.Cpu == cpu && artifact.Type == macho.TypeExec
	} else {
		artifact, parseErr := elf.NewFile(f)
		valid = parseErr == nil && artifact.Class == elf.ELFCLASS64 && artifact.Machine == machine &&
			(artifact.Type == elf.ET_EXEC || artifact.Type == elf.ET_DYN)
	}
	if !valid {
		serverutil.WriteError(w, http.StatusInternalServerError, "INVALID_BINARY", "Bridge artifact does not match "+platform+"/"+arch)
		return
	}
	w.Header().Set("Content-Type", "application/octet-stream")
	w.Header().Set("Content-Disposition", `attachment; filename="`+name+`"`)
	// Use the validated open file so an atomic deployment cannot swap the file
	// between architecture validation and transfer. ServeContent supports resume.
	http.ServeContent(w, r, name, info.ModTime(), f)
}
