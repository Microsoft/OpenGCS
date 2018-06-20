package main

import (
	"flag"
	"fmt"
	"io"
	"os"
	"os/signal"
	"path/filepath"
	"runtime"
	"sync"

	"github.com/Microsoft/opengcs/service/gcs/bridge"
	"github.com/Microsoft/opengcs/service/gcs/core/gcs"
	"github.com/Microsoft/opengcs/service/gcs/oslayer/realos"
	"github.com/Microsoft/opengcs/service/gcs/runtime/runc"
	"github.com/Microsoft/opengcs/service/gcs/transport"
	"github.com/sirupsen/logrus"
	"golang.org/x/sys/unix"
)

func main() {
	logLevel := flag.String("loglevel", "debug", "Logging Level: debug, info, warning, error, fatal, panic.")
	logFile := flag.String("logfile", "", "Logging Target: An optional file name/path. Omit for console output.")
	logFormat := flag.String("log-format", "text", "Logging Format: text or json")
	useInOutErr := flag.Bool("use-inouterr", false, "If true use stdin/stdout for bridge communication and stderr for logging")

	flag.Usage = func() {
		fmt.Fprintf(os.Stderr, "\nUsage of %s:\n", os.Args[0])
		flag.PrintDefaults()
		fmt.Fprintf(os.Stderr, "Examples:\n")
		fmt.Fprintf(os.Stderr, "    %s -loglevel=debug -logfile=/tmp/gcs.log\n", os.Args[0])
		fmt.Fprintf(os.Stderr, "    %s -loglevel=info -logfile=stdout\n", os.Args[0])
	}

	flag.Parse()

	// Use a file instead of stdout
	if *logFile != "" {
		logFileHandle, err := os.OpenFile(*logFile, os.O_WRONLY|os.O_CREATE|os.O_TRUNC, 0600)
		if err != nil {
			logrus.Fatalf("failed to create log file %s", *logFile)
		}
		logrus.SetOutput(logFileHandle)
	}

	switch *logFormat {
	case "text":
		// retain logrus's default.
	case "json":
		logrus.SetFormatter(new(logrus.JSONFormatter))
	default:
		logrus.Fatalf("unknown log-format %q", *logFormat)
	}

	level, err := logrus.ParseLevel(*logLevel)
	if err != nil {
		logrus.Fatal(err)
	}

	logrus.SetLevel(level)

	baseLogPath := "/tmp/gcs"
	baseStoragePath := "/tmp"

	// Setup the ability to dump the go stacks if the process is signaled.
	sigChan := make(chan os.Signal, 1)
	defer close(sigChan)

	signal.Notify(sigChan, unix.SIGUSR1)
	go func() {
		if err := os.MkdirAll(baseLogPath, 0700); err != nil {
			logrus.Errorf("failed to create base directory to write signal info err (%s)", err)
			return
		}

		for range sigChan {
			var buf []byte
			var stackSize int
			bufStartLen := 10240 // 10 MB

			// Continually grow the buffer until we have enough space to capture
			// the entire stack.
			for stackSize == len(buf) {
				buf = make([]byte, bufStartLen)
				stackSize = runtime.Stack(buf, true)
				bufStartLen *= 2
			}
			buf = buf[:stackSize]

			path := filepath.Join(baseLogPath, "gcs-stacks.log")
			if stackFile, err := os.OpenFile(path, os.O_CREATE|os.O_TRUNC|os.O_WRONLY, 0600); err != nil {
				logrus.Errorf("failed to create stacks file (%s) to write signal info err (%s)", path, err)
				continue
			} else {
				writeWg := sync.WaitGroup{}
				writeWg.Add(1)
				go func() {
					defer writeWg.Done()
					defer stackFile.Close()
					defer stackFile.Sync()

					if _, err := stackFile.Write(buf); err != nil {
						logrus.Errorf("failed to write stacks data to file (%s)", err)
					}
				}()

				writeWg.Wait()
			}
		}
	}()

	logrus.Info("GCS started")
	tport := &transport.VsockTransport{}
	rtime, err := runc.NewRuntime(baseLogPath)
	if err != nil {
		logrus.Fatalf("%+v", err)
	}
	ros := realos.NewOS()
	coreint := gcs.NewGCSCore(baseLogPath, baseStoragePath, rtime, ros, tport)
	mux := bridge.NewBridgeMux()
	b := bridge.Bridge{
		Handler: mux,
	}
	h := gcs.NewHost(rtime, ros, tport)
	b.AssignHandlers(mux, coreint, h)

	var bridgeIn io.Reader
	var bridgeOut io.Writer
	if *useInOutErr {
		bridgeIn = os.Stdin
		bridgeOut = os.Stdout
	} else {
		const commandPort uint32 = 0x40000000
		bridgeCon, err := tport.Dial(commandPort)
		if err != nil {
			logrus.Fatal(err)
		}
		bridgeIn = bridgeCon
		bridgeOut = bridgeCon
		logrus.Info("main: successfully connected to the HCS via HyperV_Socket")
	}

	err = b.ListenAndServe(bridgeIn, bridgeOut)
	if err != nil {
		logrus.Fatal(err)
	}
}
