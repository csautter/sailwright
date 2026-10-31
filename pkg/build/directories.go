package build

import (
	"fmt"
	"log"
	"os"
	"path/filepath"
	"runtime"
)

const (
	devAlchemyAppName           = "dev-alchemy"
	devAlchemyAppDataEnvVar     = "DEV_ALCHEMY_APP_DATA_DIR"
	devAlchemyConfigEnvVar      = "DEV_ALCHEMY_CONFIG_DIR"
	devAlchemyCacheEnvVar       = "DEV_ALCHEMY_CACHE_DIR"
	devAlchemyVagrantEnvVar     = "DEV_ALCHEMY_VAGRANT_DIR"
	devAlchemyPackerCacheEnvVar = "DEV_ALCHEMY_PACKER_CACHE_DIR"
	managedDirPermission        = 0o700
)

type Directories struct {
	WorkingDir     string
	ProjectDir     string
	AppDataDir     string
	ConfigDir      string
	CacheDir       string
	VagrantDir     string
	PackerCacheDir string
}

var (
	directoriesInstance *Directories
)

func GetDirectoriesInstance() *Directories {
	if directoriesInstance == nil {
		directoriesInstance = &Directories{}
		directoriesInstance.GetDirectories()
	}
	return directoriesInstance
}

func (u *Directories) GetDirectories() Directories {
	if u.WorkingDir == "" {
		if u.getWorkingDir() == "" {
			log.Fatalf("Working dir could not be determined.")
		}
	}
	if u.AppDataDir == "" {
		appDataDir, err := resolveDefaultAppDataDir()
		if err != nil {
			log.Fatalf("App data dir could not be determined: %v", err)
		}
		u.AppDataDir = appDataDir
	}
	if u.ProjectDir == "" {
		projectDir, err := ensureProjectDir(u.WorkingDir, u.AppDataDir)
		if err != nil {
			log.Fatalf("Project dir could not be determined: %v", err)
		}
		u.ProjectDir = projectDir
	}
	if u.ConfigDir == "" {
		configDir, err := resolveDefaultConfigDir()
		if err != nil {
			log.Fatalf("Config dir could not be determined: %v", err)
		}
		u.ConfigDir = configDir
	}
	if u.CacheDir == "" {
		u.CacheDir = directoryOverride(devAlchemyCacheEnvVar, filepath.Join(u.AppDataDir, "cache"))
	}
	if u.VagrantDir == "" {
		u.VagrantDir = directoryOverride(devAlchemyVagrantEnvVar, filepath.Join(u.AppDataDir, ".vagrant"))
	}
	if u.PackerCacheDir == "" {
		u.PackerCacheDir = directoryOverride(devAlchemyPackerCacheEnvVar, filepath.Join(u.AppDataDir, "packer_cache"))
	}
	if err := ensureDirectoriesExist(u.AppDataDir, u.ConfigDir, u.CacheDir, u.VagrantDir, u.PackerCacheDir); err != nil {
		log.Fatalf("Managed application directories could not be created: %v", err)
	}
	return *u
}

func (u *Directories) getWorkingDir() string {
	if u.WorkingDir != "" {
		return u.WorkingDir
	}
	dir, err := os.Getwd()
	if err != nil {
		return ""
	}
	u.WorkingDir = dir
	return dir
}

func (u *Directories) CachePath(paths ...string) string {
	u.GetDirectories()
	return filepath.Join(append([]string{u.CacheDir}, paths...)...)
}

func (u *Directories) ConfigPath(paths ...string) string {
	u.GetDirectories()
	return filepath.Join(append([]string{u.ConfigDir}, paths...)...)
}

func (u *Directories) VagrantPath(paths ...string) string {
	u.GetDirectories()
	return filepath.Join(append([]string{u.VagrantDir}, paths...)...)
}

func (u *Directories) PackerCachePath(paths ...string) string {
	u.GetDirectories()
	return filepath.Join(append([]string{u.PackerCacheDir}, paths...)...)
}

func (u *Directories) ManagedEnv() []string {
	u.GetDirectories()
	return []string{
		devAlchemyAppDataEnvVar + "=" + u.AppDataDir,
		devAlchemyConfigEnvVar + "=" + u.ConfigDir,
		devAlchemyCacheEnvVar + "=" + u.CacheDir,
		devAlchemyVagrantEnvVar + "=" + u.VagrantDir,
		devAlchemyPackerCacheEnvVar + "=" + u.PackerCacheDir,
		"PACKER_CACHE_DIR=" + u.PackerCacheDir,
	}
}

func resolveDefaultAppDataDir() (string, error) {
	return resolveDefaultAppDataDirForOS(runtime.GOOS, os.Getenv, os.UserHomeDir, os.UserConfigDir)
}

func resolveDefaultConfigDir() (string, error) {
	return resolveDefaultConfigDirForOS(runtime.GOOS, os.Getenv, os.UserHomeDir, os.UserConfigDir)
}

func resolveDefaultAppDataDirForOS(
	goos string,
	getenv func(string) string,
	userHomeDir func() (string, error),
	userConfigDir func() (string, error),
) (string, error) {
	if override := getenv(devAlchemyAppDataEnvVar); override != "" {
		return filepath.Clean(override), nil
	}

	switch goos {
	case "darwin":
		homeDir, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve macOS home directory: %w", err)
		}
		return filepath.Join(homeDir, "Library", "Application Support", devAlchemyAppName), nil
	case "windows":
		if localAppData := getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, devAlchemyAppName), nil
		}
		if appData := getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, devAlchemyAppName), nil
		}
		configDir, err := userConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve Windows app data directory: %w", err)
		}
		return filepath.Join(configDir, devAlchemyAppName), nil
	default:
		if xdgDataHome := getenv("XDG_DATA_HOME"); xdgDataHome != "" {
			return filepath.Join(xdgDataHome, devAlchemyAppName), nil
		}
		homeDir, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve Linux home directory: %w", err)
		}
		return filepath.Join(homeDir, ".local", "share", devAlchemyAppName), nil
	}
}

func resolveDefaultConfigDirForOS(
	goos string,
	getenv func(string) string,
	userHomeDir func() (string, error),
	userConfigDir func() (string, error),
) (string, error) {
	if override := getenv(devAlchemyConfigEnvVar); override != "" {
		return filepath.Clean(override), nil
	}

	switch goos {
	case "darwin":
		homeDir, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve macOS home directory: %w", err)
		}
		return filepath.Join(homeDir, "Library", "Application Support", devAlchemyAppName), nil
	case "windows":
		if appData := getenv("APPDATA"); appData != "" {
			return filepath.Join(appData, devAlchemyAppName), nil
		}
		if localAppData := getenv("LOCALAPPDATA"); localAppData != "" {
			return filepath.Join(localAppData, devAlchemyAppName), nil
		}
		configDir, err := userConfigDir()
		if err != nil {
			return "", fmt.Errorf("resolve Windows config directory: %w", err)
		}
		return filepath.Join(configDir, devAlchemyAppName), nil
	default:
		if xdgConfigHome := getenv("XDG_CONFIG_HOME"); xdgConfigHome != "" {
			return filepath.Join(xdgConfigHome, devAlchemyAppName), nil
		}
		homeDir, err := userHomeDir()
		if err != nil {
			return "", fmt.Errorf("resolve Linux home directory: %w", err)
		}
		return filepath.Join(homeDir, ".config", devAlchemyAppName), nil
	}
}

func ensureDirectoriesExist(paths ...string) error {
	for _, currentPath := range paths {
		if currentPath == "" {
			continue
		}
		if err := os.MkdirAll(currentPath, managedDirPermission); err != nil {
			return err
		}
	}
	return nil
}

func directoryOverride(name, fallback string) string {
	if value := os.Getenv(name); value != "" {
		return filepath.Clean(value)
	}
	return fallback
}
