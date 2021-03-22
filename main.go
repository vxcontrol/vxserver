package main

import (
	"encoding/json"
	"flag"
	"fmt"
	"io"
	"io/ioutil"
	"os"
	"path/filepath"
	"runtime"
	"sync"
	"time"

	"github.com/judwhite/go-svc"
	"github.com/sirupsen/logrus"
	"github.com/takama/daemon"

	"github.com/vxcontrol/vxcommon/controller"
	"github.com/vxcontrol/vxcommon/db"
	"github.com/vxcontrol/vxcommon/utils"

	"github.com/vxcontrol/vxserver/mmodule"
)

const (
	name        = "vxserver"
	description = "VXServer service to agents control"
)

// PackageVer is semantic version of vxserver
var PackageVer string

// PackageRev is revision of vxserver build
var PackageRev string

// config implements struct of vxserver configuration file
type config struct {
	Loader struct {
		Config string `json:"config"`
		Files  string `json:"files"`
	} `json:"loader"`
	DB struct {
		Host string `json:"host"`
		Port string `json:"port"`
		Name string `json:"name"`
		User string `json:"user"`
		Pass string `json:"pass"`
	} `json:"db"`
	S3 struct {
		AccessKey  string `json:"access_key"`
		SecretKey  string `json:"secret_key"`
		BucketName string `json:"bucket_name"`
		Endpoint   string `json:"endpoint"`
	} `json:"s3"`
	Base   string `json:"base"`
	LogDir string `json:"log_dir"`
	Listen string `json:"listen"`
}

// Server implements daemon structure
type Server struct {
	debug        bool
	service      bool
	logDir       string
	command      string
	listen       string
	basePath     string
	configLoader string
	filesLoader  string
	module       *mmodule.MainModule
	wg           sync.WaitGroup
	svc          daemon.Daemon
}

// Init for preparing server main module struct
func (s *Server) Init(env svc.Environment) (err error) {
	var gdb *db.DB
	if s.configLoader == "db" {
		gdb, err = db.New()
		if err != nil {
			logrus.WithError(err).Error("failed to initialize connection to DB")
			return
		}

		err = gdb.MigrateUp("migrations")
		if err != nil {
			logrus.WithError(err).Error("failed to migrate current DB")
			return
		}
		logrus.Info("DB migration was completed")
	}

	var cl controller.IConfigLoader
	switch s.configLoader {
	case "fs":
		cl, err = controller.NewConfigFromFS(s.basePath)
	case "s3":
		cl, err = controller.NewConfigFromS3()
	case "db":
		cl, err = controller.NewConfigFromDB()
	default:
		err = fmt.Errorf("unknown confuration loader type")
	}
	if err != nil {
		logrus.WithError(err).Error("failed to initialize config loader")
		return
	}
	logrus.Info("modules configuration loader was created")

	var fl controller.IFilesLoader
	switch s.filesLoader {
	case "fs":
		fl, err = controller.NewFilesFromFS(s.basePath)
	case "s3":
		fl, err = controller.NewFilesFromS3()
	default:
		err = fmt.Errorf("unknown files loader type")
	}
	if err != nil {
		logrus.WithError(err).Error("failed initialize files loader")
		return
	}
	logrus.Info("modules files loader was created")

	utils.RemoveUnusedTempDir()
	if s.module, err = mmodule.New(s.listen, cl, fl, gdb); err != nil {
		logrus.WithError(err).Error("failed to initialize main module")
		return
	}
	logrus.Info("main module was created")

	return
}

// Start logic of server main module
func (s *Server) Start() (err error) {
	logrus.Info("vxserver is starting...")
	s.wg.Add(1)
	go func() {
		defer s.wg.Done()
		err = s.module.Start()
	}()

	// Wait a little time to catch error on start
	time.Sleep(time.Second)
	logrus.Info("vxserver started")

	return
}

// Stop logic of server main module
func (s *Server) Stop() (err error) {
	logrus.Info("vxserver is stopping...")
	if err = s.module.Stop(); err != nil {
		return
	}
	logrus.Info("vxserver is waiting of modules release...")
	s.wg.Wait()
	logrus.Info("vxserver stopped")

	return
}

// storeConfig is storing of current server configuration to file
func (s *Server) storeConfig() (string, error) {
	cfg := config{
		Base:   s.basePath,
		LogDir: s.logDir,
		Listen: s.listen,
	}
	cfg.Loader.Files = s.filesLoader
	cfg.Loader.Config = s.configLoader
	cfg.DB.Host = os.Getenv("DB_HOST")
	cfg.DB.Port = os.Getenv("DB_PORT")
	cfg.DB.Name = os.Getenv("DB_NAME")
	cfg.DB.User = os.Getenv("DB_USER")
	cfg.DB.Pass = os.Getenv("DB_PASS")
	cfg.S3.AccessKey = os.Getenv("MINIO_ACCESS_KEY")
	cfg.S3.SecretKey = os.Getenv("MINIO_SECRET_KEY")
	cfg.S3.BucketName = os.Getenv("MINIO_BUCKET_NAME")
	cfg.S3.Endpoint = os.Getenv("MINIO_ENDPOINT")

	cfgDir, err := filepath.Abs(filepath.Dir(os.Args[0]))
	if err != nil {
		return "", err
	}
	cfgPath := filepath.Join(cfgDir, "config.json")
	cfgData, _ := json.MarshalIndent(cfg, "", " ")
	if err := ioutil.WriteFile(cfgPath, cfgData, 0644); err != nil {
		return "", err
	}

	return cfgPath, nil
}

// storeConfig is parsing server configuration from file
func (s *Server) parseConfig(path string) error {
	var cfg config
	cfgData, err := ioutil.ReadFile(path)
	if err != nil {
		return err
	}
	if err := json.Unmarshal(cfgData, &cfg); err != nil {
		return err
	}

	s.logDir = cfg.LogDir
	s.listen = cfg.Listen
	s.basePath = cfg.Base
	s.filesLoader = cfg.Loader.Files
	s.configLoader = cfg.Loader.Config
	os.Setenv("DB_HOST", cfg.DB.Host)
	os.Setenv("DB_PORT", cfg.DB.Port)
	os.Setenv("DB_NAME", cfg.DB.Name)
	os.Setenv("DB_USER", cfg.DB.User)
	os.Setenv("DB_PASS", cfg.DB.Pass)
	os.Setenv("MINIO_ACCESS_KEY", cfg.S3.AccessKey)
	os.Setenv("MINIO_SECRET_KEY", cfg.S3.SecretKey)
	os.Setenv("MINIO_BUCKET_NAME", cfg.S3.BucketName)
	os.Setenv("MINIO_ENDPOINT", cfg.S3.Endpoint)

	return nil
}

// Manage by daemon commands or run the daemon
func (s *Server) Manage() (string, error) {
	switch s.command {
	case "install":
		configPath, err := s.storeConfig()
		if err != nil {
			logrus.WithError(err).Error("failed to store config file")
			return "vxserver service install failed", err
		}
		opts := []string{
			"-service",
			"-config", configPath,
		}
		if s.debug {
			opts = append(opts, "-debug")
		}
		return s.svc.Install(opts...)
	case "uninstall":
		return s.svc.Remove()
	case "start":
		return s.svc.Start()
	case "stop":
		return s.svc.Stop()
	case "status":
		return s.svc.Status()
	}

	if err := svc.Run(s); err != nil {
		logrus.WithError(err).Error("vxserver executing failed")
		return "vxserver running failed", err
	}

	logrus.Info("vxserver exited normaly")
	return "vxserver exited normaly", nil
}

func main() {
	var server Server
	var version bool
	var config string
	flag.StringVar(&server.listen, "listen", "ws://localhost:8080", "Listen IP:Port")
	flag.StringVar(&server.basePath, "path", "./modules", "Path to modules directory")
	flag.StringVar(&server.configLoader, "mcl", "fs", "Mode of config loader: [fs, s3, db]")
	flag.StringVar(&server.filesLoader, "mfl", "fs", "Mode of files loader: [fs, s3]")
	flag.StringVar(&config, "config", "", "Path to server config file")
	flag.StringVar(&server.command, "command", "", `Command to service control (not required):
  install - install the service to the system
  uninstall - uninstall the service from the system
  start - start the service
  stop - stop the service
  status - status of the service`)
	flag.StringVar(&server.logDir, "logdir", "", "System option to define log directory to vxserver")
	flag.BoolVar(&server.debug, "debug", false, "System option to run vxserver in debug mode")
	flag.BoolVar(&server.service, "service", false, "System option to run vxserver as a service")
	flag.BoolVar(&version, "version", false, "Print current version of vxserver and exit")
	flag.Parse()

	if version {
		fmt.Printf("vxserver version is ")
		if PackageVer != "" {
			fmt.Printf("%s", PackageVer)
		} else {
			fmt.Printf("develop")
		}
		if PackageRev != "" {
			fmt.Printf("-%s\n", PackageRev)
		} else {
			fmt.Printf("\n")
		}

		os.Exit(0)
	}

	switch server.command {
	case "install":
	case "uninstall":
	case "start":
	case "stop":
	case "status":
	case "":
	default:
		fmt.Println("invalid value of 'command' argument: ", server.command)
		flag.PrintDefaults()
		os.Exit(1)
	}

	if config != "" {
		if err := server.parseConfig(config); err != nil {
			logrus.WithError(err).Error("vxserver config parse failed")
			os.Exit(1)
		}
	}

	if os.Getenv("LISTEN") != "" {
		server.listen = os.Getenv("LISTEN")
	}
	if os.Getenv("BASE_PATH") != "" {
		server.basePath = os.Getenv("BASE_PATH")
	}
	if os.Getenv("CONFIG_LOADER") != "" {
		server.configLoader = os.Getenv("CONFIG_LOADER")
	}
	if os.Getenv("FILES_LOADER") != "" {
		server.filesLoader = os.Getenv("FILES_LOADER")
	}
	if os.Getenv("LOG_DIR") != "" {
		server.logDir = os.Getenv("LOG_DIR")
	}
	if os.Getenv("DEBUG") != "" {
		server.debug = true
	}

	switch server.configLoader {
	case "fs":
	case "s3":
	case "db":
	default:
		fmt.Println("invalid value of 'mcl' argument: ", server.configLoader)
		flag.PrintDefaults()
		os.Exit(1)
	}

	switch server.filesLoader {
	case "fs":
	case "s3":
	default:
		fmt.Println("invalid value of 'mfl' argument: ", server.filesLoader)
		flag.PrintDefaults()
		os.Exit(1)
	}

	if server.logDir == "" {
		server.logDir = filepath.Dir(os.Args[0])
	}
	logDir, err := filepath.Abs(server.logDir)
	if err != nil {
		fmt.Println("invalid value of 'logdir' argument: ", server.logDir)
		os.Exit(1)
	} else {
		server.logDir = logDir
	}

	if server.debug {
		logrus.SetLevel(logrus.DebugLevel)
	} else {
		logrus.SetLevel(logrus.InfoLevel)
	}

	logPath := filepath.Join(server.logDir, "server.log")
	logFile, err := os.OpenFile(logPath, os.O_APPEND|os.O_CREATE|os.O_WRONLY, 0644)
	if err != nil {
		fmt.Println("failed to open log file: ", logPath)
		os.Exit(1)
	}
	if server.service {
		logrus.SetOutput(logFile)
	} else {
		logrus.SetOutput(io.MultiWriter(os.Stdout, logFile))
	}

	kind := daemon.SystemDaemon
	var dependencies []string
	switch runtime.GOOS {
	case "linux":
		dependencies = []string{"multi-user.target", "sockets.target"}
	case "windows":
		dependencies = []string{"tcpip"}
	case "darwin":
		if os.Geteuid() == 0 {
			kind = daemon.GlobalDaemon
		} else {
			kind = daemon.UserAgent
		}
	default:
		logrus.Error("unsupported OS type")
		os.Exit(1)
	}

	server.svc, err = daemon.New(name, description, kind, dependencies...)
	if err != nil {
		logrus.WithError(err).Error("vxserver service creating failed")
		os.Exit(1)
	}

	status, err := server.Manage()
	if err != nil {
		fmt.Println(status, "\n", err.Error())
		os.Exit(1)
	}
	fmt.Println(status)
}
