package concurrence

import (
	"context"
	"encoding/json"
	"fmt"
	"io/ioutil"
	"log"
	"net/http"
	"strings"
	"time"

	"github.com/livepeer/stream-tester/model"
)

// Tester maintains each individual test run
type Tester struct {
	httpClient *http.Client
	host       string
	port       uint16
	rtmpHost   string
	rtmpPort   uint16
	mediaHost  string
	mediaPort  uint16

	checkInterval  time.Duration
	minSuccessRate float64
	numStreamsInit uint
	numStreamsStep uint

	baseManifestID string
	done           bool

	alert func(s string, r *Result)
}

// NewTester creates a new Tester
func NewTester(httpClient *http.Client, host, rtmpHost, mediaHost string, port, rtmpPort, mediaPort uint, checkInterval time.Duration, minSuccessRate float64, numStreamsInit, numStreamsStep uint, alert func(string, *Result)) *Tester {
	t := &Tester{
		httpClient:     httpClient,
		host:           host,
		port:           uint16(port),
		rtmpHost:       rtmpHost,
		rtmpPort:       uint16(rtmpPort),
		mediaHost:      mediaHost,
		mediaPort:      uint16(mediaPort),
		checkInterval:  checkInterval,
		minSuccessRate: minSuccessRate,
		numStreamsInit: numStreamsInit,
		numStreamsStep: numStreamsStep,
		alert:          alert,
	}
	return t
}

// IsRunning returns true if the benchmark is running, and false otherwise
func (t *Tester) IsRunning() bool { return t.baseManifestID != "" && !t.done }

// GetManifestID returns the manifest ID of the stream in progress
func (t *Tester) GetManifestID() string { return t.baseManifestID }

func (t *Tester) start(NumStreams uint) (*model.StartStreamsRes, error) {
	response := &model.StartStreamsRes{}

	url := fmt.Sprintf("http://%s:%d/start_streams", t.host, t.port)
	contentType := "application/json"

	startStreamReq := model.StartStreamsReq{
		Host:           t.rtmpHost,
		MHost:          t.mediaHost,
		FileName:       "official_test_source_2s_keys_24pfs.mp4",
		RTMP:           t.rtmpPort,
		Media:          t.mediaPort,
		Repeat:         1,
		Simultaneous:   NumStreams,
		ProfilesNum:    2,
		MeasureLatency: false,
		HTTPIngest:     false,
	}

	startStreamReqBytes, err := json.Marshal(startStreamReq)
	if err != nil {
		log.Printf("json.Marshall failed: %v", err)
	}
	postBody := strings.NewReader(string(startStreamReqBytes))

	resp, err := t.httpClient.Post(url, contentType, postBody)
	if err != nil {
		log.Printf("start() failed: %v", err)
		return response, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("ReadAll failed: %v", err)
		return response, err
	}

	err = json.Unmarshal(body, response)
	if err != nil {
		log.Printf("Unmarshal failed: %v", err)
		return response, err
	}

	t.baseManifestID = response.BaseManifestID

	log.Printf("start: %v", string(body))

	return response, nil
}

func (t *Tester) stop() error {
	url := fmt.Sprintf("http://%s:%d/stop", t.host, t.port)
	_, err := t.httpClient.Get(url)
	if err != nil {
		log.Printf("stop() failed: %v", err)
	}
	return err
}

func (t *Tester) stats() (*model.Stats, error) {
	stats := &model.Stats{}
	url := fmt.Sprintf("http://%s:%d/stats?latencies&base_manifest_id=%s", t.host, t.port, t.baseManifestID)

	resp, err := t.httpClient.Get(url)
	if err != nil {
		log.Printf("stats() failed: %v", err)
		return stats, err
	}
	defer resp.Body.Close()

	body, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		log.Printf("ReadAll failed: %v", err)
		return stats, err
	}

	err = json.Unmarshal(body, stats)
	if err != nil {
		log.Printf("Unmarshal failed: %v", err)
		return stats, err
	}

	log.Printf("stats: %v", string(body))

	return stats, nil
}

// Result contains the results of the Run() function
type Result struct {
	NumStreams uint
	Stats      *model.Stats
}

// Run executes the stream concurrency benchmark and returns the Result
func (t *Tester) Run(ctx context.Context) (*Result, error) {
	NumStreams := t.numStreamsInit
	t.done = false

	// if we can, subtract the step size from initial count so that the
	// requested init value is actually tested, otherwise just go as-is
	if t.numStreamsInit > t.numStreamsStep {
		NumStreams = t.numStreamsInit - t.numStreamsStep
	}

	stats := &model.Stats{}

	started := false
	for !started || stats.SuccessRate > t.minSuccessRate {

		NumStreams = NumStreams + t.numStreamsStep
		started = true

		t.alert("begin iteration: ", &Result{NumStreams, stats})

		select {
		case <-ctx.Done():
			return &Result{}, fmt.Errorf("context cancelled")
		default:
		}

		startStreamResponse, err := t.start(NumStreams)
		if err != nil {
			return &Result{}, fmt.Errorf("start stream failed: %v", err)
		}
		if !startStreamResponse.Success {
			return &Result{}, fmt.Errorf("start stream failed: success = %v", startStreamResponse.Success)
		}

		c := time.NewTicker(t.checkInterval)

	loop:
		for {
			select {
			case <-ctx.Done():
				return &Result{}, fmt.Errorf("context cancelled")
			case <-c.C:
				stats, err = t.stats()
				if err != nil {
					return &Result{}, fmt.Errorf("get stats failed: %v", err)
				}

				if stats.Finished {
					break loop
				}
			}
		}

		log.Printf("%d streams: %v", NumStreams, stats)
	}
	t.done = true
	log.Printf("success degrades at %d streams: %v", NumStreams, stats.SuccessRate)
	return &Result{NumStreams, stats}, nil
}
