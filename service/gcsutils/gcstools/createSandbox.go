package main

import (
	"flag"
	"io"
	"os"

	"github.com/Microsoft/opengcs/service/gcsutils/gcstools/commoncli"
	"github.com/Microsoft/opengcs/service/libs/commonutils"
)

const PreBuiltSandboxFile = "/root/integration/prebuildSandbox.vhdx"

func createSandbox() error {
	logArgs := commoncli.SetFlagsForLogging()
	sandboxLocation := flag.String("file", PreBuiltSandboxFile, "Sandbox file location")
	size := flag.Int64("size", 20*1024*1024*1024, "20GB in bytes")
	flag.Parse()

	if err := commoncli.SetupLogging(logArgs...); err != nil {
		return err
	}

	utils.LogMsgf("Got location=%s and size=%d\n", sandboxLocation, *size)
	file, err := os.Open(*sandboxLocation)
	if err != nil {
		utils.LogMsgf("Error opening %s: %s\n", *sandboxLocation, err)
		return err
	}
	defer file.Close()

	if _, err = io.Copy(os.Stdout, file); err != nil {
		utils.LogMsgf("Error copying %s: %s", *sandboxLocation, err)
		return err
	}

	return nil
}

func createSandbox_main() {
	if err := createSandbox(); err != nil {
		os.Exit(1)
	}
	os.Exit(0)
}
