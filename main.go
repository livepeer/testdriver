package main

import (
	"context"
	"flag"
	"fmt"
	"log"
	"net/http"
	"time"

	"github.com/labstack/echo"
	"github.com/labstack/echo/middleware"

	"github.com/livepeer/stream-tester/messenger"
	"github.com/mk-livepeer/testdriver/concurrence"
)

func main() {
	log.Println("testdriver started")
	defer log.Println("testdriver stopped")

	streamTesterHost := flag.String("tester-host", "streamtester", "stream-tester hostname")
	streamTesterPort := flag.Uint("tester-port", 7934, "stream-tester http port")
	rtmpHost := flag.String("rtmp-host", "broadcaster", "rtmp hostname")
	rtmpPort := flag.Uint("rtmp-port", 1935, "rtmp port")
	mediaHost := flag.String("media-host", "broadcaster", "media hostname")
	mediaPort := flag.Uint("media-port", 8935, "media port")

	startDelaySec := flag.Int64("start-delay", 10, "time delay in seconds before start")
	startDelay := time.Duration(*startDelaySec) * time.Second

	statsIntervalSec := flag.Int64("stats-interval", 10, "time interval in seconds between checking stats")
	statsInterval := time.Duration(*statsIntervalSec) * time.Second

	minSuccessRate := flag.Float64("min-success-rate", 99.0, "minimum success rate required to continue testing an increased number of streams")
	numProfiles := flag.Uint("profiles", 1, "number of profiles to test")
	numStreamsInit := flag.Uint("streams-init", 1, "number of streams to begin tests with")
	numStreamsStep := flag.Uint("streams-step", 1, "number of streams to increase by on each successive test")

	discordURL := flag.String("discord-url", "", "URL of Discord's webhook to send messages to Discord channel")
	discordUserName := flag.String("discord-user-name", "", "User name to use when sending messages to Discord")
	discordUsersToNotify := flag.String("discord-users", "", "User IDs to notify in case of failure")

	httpPort := flag.Int("http-port", 80, "testdriver http port")

	flag.Parse()

	messenger.Init(*discordURL, *discordUserName, *discordUsersToNotify)

	httpClient := &http.Client{
		Timeout: 3 * time.Second,
	}
	testDriver := concurrence.NewTester(
		httpClient,
		*streamTesterHost,
		*rtmpHost,
		*mediaHost,
		*streamTesterPort,
		*rtmpPort,
		*mediaPort,
		statsInterval,
		*minSuccessRate,
		*numStreamsInit,
		*numStreamsStep,
		func(s string, r *concurrence.Result) {
			messenger.SendMessage(fmt.Sprintf("%s rtmp host: %s:%d / media host: %s:%d using streamtester host: %s:%d - now testing %d concurrent streams with %d profiles",
				s, *rtmpHost, *rtmpPort, *mediaHost, *mediaPort, *streamTesterHost, *streamTesterPort, r.NumStreams, r.NumProfiles))
		},
	)

	resultString := func(res *concurrence.Result) string {
		return fmt.Sprintf(
			"concurrency test results for rtmp host: %s:%d / media host: %s:%d using streamtester host: %s:%d - success rate degrades at %d streams: %v",
			*rtmpHost, *rtmpPort, *mediaHost, *mediaPort, *streamTesterHost, *streamTesterPort, res.NumStreams, res.Stats)
	}

	// channel for catching benchmark results to be served on endpoint
	ch := make(chan *concurrence.Result)

	if *httpPort > 0 {
		go func() {
			e := echo.New()

			e.Use(middleware.Logger())
			e.Use(middleware.Recover())

			var res *concurrence.Result

			go func() { res = <-ch }()

			// Route => handler
			e.GET("/", func(c echo.Context) error {
				if testDriver.IsRunning() {
					return c.String(http.StatusOK, "Benchmark is currently running.\n")
				}
				if testDriver.GetManifestID() == "" {
					return c.String(http.StatusOK, "Benchmark is not running.\n")
				}
				return c.String(http.StatusOK, resultString(res))
			})

			// Start server
			e.Logger.Fatal(e.Start(fmt.Sprintf(":%d", *httpPort)))
		}()
	}

	t := time.NewTicker(startDelay)
	<-t.C

	// FIXME: add random generated string to use as unique identifier
	messenger.SendMessage(
		fmt.Sprintf(
			"concurrency test started for rtmp host: %s:%d / media host: %s:%d using streamtester host: %s:%d",
			*rtmpHost, *rtmpPort, *mediaHost, *mediaPort, *streamTesterHost, *streamTesterPort))

	ctx := context.Background()

	res, err := testDriver.Run(ctx, *numProfiles)
	if err != nil {
		messenger.SendFatalMessage(fmt.Sprintf("FAILED: %v", err))
		log.Fatalf("FAILED: %v", err)
	}

	// send res into channel async
	go func() { ch <- res }()

	messenger.SendMessage(resultString(res))
}
