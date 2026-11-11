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
	showCapabilities := flag.Bool("capabilities", false, "print machine-readable policy capabilities")
	sampleActivity := flag.Bool("sample-activity", false, "print read-only recent NVML activity")
	showVersion := flag.Bool("version", false, "print version and exit")
	release := flag.Bool("release", false, "release P-state, fan and clock controls, then exit")
	controlDirectory := flag.String("control-dir", env("CMP50_POWER_CONTROL_DIR", "/var/lib/cmp-idle-governor"), "AI Server Manager CMP50 control directory")
	flag.Parse()
	if *showCapabilities {
		fmt.Println(`{"policy":"clock-stages","private_auto_pstate":false,"forced_pstates":false,"p8_enabled":false,"p8_disabled_reason":"CMP50_GSP_WAKE_FAILURE"}`)
		return
	}
	if *showVersion {
		fmt.Println(version)
		return
	}
	poll, err := envInt("CMP_POLL", 1)
	if err != nil {
		log.Fatal(err)
	}
	if *sampleActivity {
		rows, err := nvidia.RecentActivity(time.Duration(poll) * time.Second)
		if err != nil {
			log.Fatal(err)
		}
		for _, row := range rows {
			fmt.Printf("%d, %d, %d\n", row.Index, row.GPU, row.Memory)
		}
		return
	}
	idleAfter, err := envInt("CMP_IDLE_AFTER", 6)
	if err != nil {
		log.Fatal(err)
	}
	utilThreshold := 0
	if value := strings.TrimSpace(os.Getenv("CMP_UTIL")); value != "" {
		utilThreshold, err = strconv.Atoi(value)
		if err != nil || utilThreshold < 0 {
			log.Fatal("CMP_UTIL must be 0..100")
		}
	}
	fanAutoTemp, err := envInt("CMP_FAN_AUTO_TEMP", 55)
	if err != nil {
		log.Fatal(err)
	}
	if utilThreshold > 100 || fanAutoTemp > 120 {
		log.Fatal("CMP_UTIL or CMP_FAN_AUTO_TEMP is out of range")
	}

	if value := os.Getenv("CMP_POWER_POLICY"); value != "" && value != "clocks" {
		log.Fatal("only the documented NVML clock policy is supported")
	}
	idleDelay, err := envInt("CMP_IDLE_DELAY_SECONDS", 30)
	if err != nil {
		log.Fatal(err)
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
		WarmClock: os.Getenv("CMP_WARM_CLOCK"), IdleDelay: time.Duration(idleDelay) * time.Second,
		ControlDirectory: *controlDirectory, Poll: time.Duration(poll) * time.Second,
		IdleAfter: idleAfter, UtilThreshold: uint32(utilThreshold), FanAutoTemp: uint32(fanAutoTemp),
		LoadClock:      os.Getenv("CMP_LOAD_CLOCK"),
		LoadCoreOffset: os.Getenv("CMP_LOAD_CORE_OFFSET"),
	})
	if err != nil {
		log.Fatal(err)
	}
	if *release {
		if err := controller.Release(); err != nil {
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
