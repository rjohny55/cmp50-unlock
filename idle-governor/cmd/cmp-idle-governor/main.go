package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"os"
	"os/signal"
	"strconv"
	"strings"
	"syscall"
	"time"

	"github.com/rjohny55/cmp50-unlock/idle-governor/internal/governor"
	"github.com/rjohny55/cmp50-unlock/idle-governor/internal/nvidia"
)

var version = "dev"

func envInt(name string, fallback int) (int, error) {
	value := strings.TrimSpace(os.Getenv(name))
	if value == "" {
		return fallback, nil
	}
	parsed, err := strconv.Atoi(value)
	if err != nil || parsed <= 0 {
		return 0, fmt.Errorf("%s must be a positive integer", name)
	}
	return parsed, nil
}

func main() {
	showVersion := flag.Bool("version", false, "print version and exit")
	release := flag.Bool("release", false, "release P-state, fan and clock controls, then exit")
	controlDirectory := flag.String("control-dir", env("CMP50_POWER_CONTROL_DIR", "/opt/stacks/ai-server-manager/data/cmp50-power"), "AI Server Manager CMP50 control directory")
	flag.Parse()
	if *showVersion {
		fmt.Println(version)
		return
	}
	poll, err := envInt("CMP_POLL", 5)
	if err != nil {
		log.Fatal(err)
	}
	idleAfter, err := envInt("CMP_IDLE_AFTER", 6)
	if err != nil {
		log.Fatal(err)
	}
	utilThreshold, err := envInt("CMP_UTIL", 5)
	if err != nil {
		log.Fatal(err)
	}
	fanAutoTemp, err := envInt("CMP_FAN_AUTO_TEMP", 55)
	if err != nil {
		log.Fatal(err)
	}
	if utilThreshold > 100 || fanAutoTemp > 120 {
		log.Fatal("CMP_UTIL or CMP_FAN_AUTO_TEMP is out of range")
	}

	hardware, err := nvidia.Open()
	if err != nil {
		log.Fatalf("initialize NVIDIA APIs: %v", err)
	}
	defer func() {
		if err := hardware.Close(); err != nil {
			log.Printf("close NVIDIA APIs: %v", err)
		}
	}()
	controller, err := governor.New(hardware, governor.Options{
		ControlDirectory: *controlDirectory, Poll: time.Duration(poll) * time.Second,
		IdleAfter: idleAfter, UtilThreshold: uint32(utilThreshold), FanAutoTemp: uint32(fanAutoTemp),
		LoadClock:      os.Getenv("CMP_LOAD_CLOCK"),
		LoadCoreOffset: os.Getenv("CMP_LOAD_CORE_OFFSET"),
	})
	if err != nil {
		log.Fatal(err)
	}
	if *release {
		if err := controller.Cleanup(); err != nil {
			log.Fatal(err)
		}
		return
	}
	ctx, stop := signal.NotifyContext(context.Background(), syscall.SIGINT, syscall.SIGTERM)
	defer stop()
	if err := controller.Run(ctx); err != nil {
		log.Fatal(err)
	}
}

func env(name, fallback string) string {
	if value := strings.TrimSpace(os.Getenv(name)); value != "" {
		return value
	}
	return fallback
}
