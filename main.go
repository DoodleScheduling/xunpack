package main

import (
	"context"
	"log"
	"os"
	"runtime"

	crossplaneapiextv1 "github.com/crossplane/crossplane/apis/apiextensions/v1"
	crossplanev1 "github.com/crossplane/crossplane/apis/pkg/v1"
	"github.com/go-logr/logr"
	"github.com/go-logr/zapr"
	"github.com/sethvargo/go-envconfig"
	flag "github.com/spf13/pflag"
	"go.uber.org/zap"
	kruntime "k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/serializer"
	"k8s.io/cli-runtime/pkg/printers"

	"github.com/doodlescheduling/xunpack/internal/parser"
)

type Config struct {
	Log struct {
		Level    string `env:"LOG_LEVEL, default=info"`
		Encoding string `env:"LOG_ENCODING, default=json"`
	}
	File         string `env:"FILE, default=/dev/stdin"`
	Output       string `env:"OUTPUT, default=/dev/stdout"`
	FailFast     bool   `env:"FAIL_FAST"`
	AllowFailure bool   `env:"ALLOW_FAILURE"`
	Workers      int    `env:"WORKERS"`
}

var (
	config = &Config{}
)

func init() {
	flag.StringVarP(&config.Log.Level, "log-level", "l", "", "Define the log level (default is warning) [debug,info,warn,error]")
	flag.StringVarP(&config.Log.Encoding, "log-encoding", "e", "", "Define the log format (default is json) [json,console]")
	flag.StringVarP(&config.File, "file", "f", "", "Path to input")
	flag.StringVarP(&config.Output, "output", "o", "", "Path to output")
	flag.BoolVar(&config.AllowFailure, "allow-failure", false, "Do not exit > 0 if an error occured")
	flag.BoolVar(&config.FailFast, "fail-fast", false, "Exit early if an error occured")
	flag.IntVar(&config.Workers, "workers", 0, "Workers used to parse manifests")
}

func main() {
	ctx := context.TODO()
	if err := envconfig.Process(ctx, config); err != nil {
		log.Fatal(err)
	}

	flag.Parse()

	if config.Workers == 0 {
		config.Workers = runtime.NumCPU()
	}

	logger, err := buildLogger()
	must(err)

	f, err := os.Open(config.File)
	must(err)

	out, err := os.OpenFile(config.Output, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0775)
	must(err)

	scheme := kruntime.NewScheme()
	_ = crossplanev1.AddToScheme(scheme)
	_ = crossplaneapiextv1.AddToScheme(scheme)
	factory := serializer.NewCodecFactory(scheme)
	decoder := factory.UniversalDeserializer()

	p := parser.Parser{
		Output:       out,
		AllowFailure: config.AllowFailure,
		FailFast:     config.FailFast,
		Workers:      config.Workers,
		Decoder:      decoder,
		Logger:       logger,
		Printer:      &printers.YAMLPrinter{},
	}

	must(p.Run(context.TODO(), f))
}

func buildLogger() (logr.Logger, error) {
	logOpts := zap.NewDevelopmentConfig()
	logOpts.Encoding = config.Log.Encoding

	err := logOpts.Level.UnmarshalText([]byte(config.Log.Level))
	if err != nil {
		return logr.Discard(), err
	}

	zapLog, err := logOpts.Build()
	if err != nil {
		return logr.Discard(), err
	}

	return zapr.NewLogger(zapLog), nil
}

func must(err error) {
	if err != nil {
		panic(err)
	}
}
