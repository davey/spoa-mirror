package main

import (
	"bufio"
	"bytes"
	"flag"
	"fmt"
	"log"
	"net"
	"net/http"
	"os"
	"runtime"
	"strings"
	"sync"
	"time"

	"github.com/negasus/haproxy-spoe-go/agent"
	"github.com/negasus/haproxy-spoe-go/logger"
	"github.com/negasus/haproxy-spoe-go/request"
)

/*
   ./spoa-mirror -listen 0.0.0.0:20009 -host https://test-system.example.com \
                 -workers 64 -queue-size 50000 -queue-block=false -debug
*/

var httpTransport = &http.Transport{
	MaxIdleConns:          10000,
	MaxIdleConnsPerHost:   10000,
	MaxConnsPerHost:       0,
	IdleConnTimeout:       90 * time.Second,
	TLSHandshakeTimeout:   1 * time.Second,
	ResponseHeaderTimeout: 1 * time.Second,
	ForceAttemptHTTP2:     true,
}

var httpClient = &http.Client{
	Transport: httpTransport,
	Timeout:   1 * time.Second,
}

var (
	listenAddr string
	mirrorhost string
	debug      bool
	verbose    bool

	workers    int
	queueSize  int
	queueBlock bool
	jobQueue   chan mirrorJob
	workersWg  sync.WaitGroup
)

// ---------- Worker-Pool ----------

type mirrorJob struct {
	method  string
	path    string
	headers string
	body    []byte
}

func startWorkerPool(n int, jobs <-chan mirrorJob) {
	workersWg.Add(n)
	for i := 0; i < n; i++ {
		go func(id int) {
			defer workersWg.Done()
			if debug {
				log.Printf("[worker %d] started", id)
			}
			for job := range jobs {
				makeHTTPRequest(job.method, job.path, job.headers, job.body)
			}
			if debug {
				log.Printf("[worker %d] stopped", id)
			}
		}(i + 1)
	}
}

func enqueue(job mirrorJob) {
	if queueBlock {
		// backpressure: block, when queue full
		jobQueue <- job
		return
	}
	// non-blocking
	select {
	case jobQueue <- job:
	default:
		if debug {
			log.Printf("queue full (size=%d): dropping mirror job %s %s", queueSize, job.method, job.path)
		}
	}
}

// ---------- main ----------

func main() {
	// Flags
	listenAddrParam := flag.String("listen", "127.0.0.1:12345", "Address where the server should listen")
	mirrorhostParam := flag.String("host", "", "Hostname where requests should be mirrored to (no trailing slash)")
	debugParam := flag.Bool("debug", false, "Enable debug mode")
	verboseParam := flag.Bool("verbose", false, "Enable verbose mode")

	workersParam := flag.Int("workers", runtime.NumCPU()*4, "Number of parallel workers")
	queueSizeParam := flag.Int("queue-size", 10000, "Size of the worker queue")
	queueBlockParam := flag.Bool("queue-block", false, "Block when queue is full instead of dropping")

	flag.Parse()

	listenAddr = *listenAddrParam
	mirrorhost = *mirrorhostParam
	debug = *debugParam
	verbose = *verboseParam
	workers = *workersParam
	queueSize = *queueSizeParam
	queueBlock = *queueBlockParam

	// Validate mirrorhost
	if mirrorhost == "" {
		log.Fatal("Error: Hostname is required")
	}
	if strings.HasSuffix(mirrorhost, "/") {
		log.Fatal("Error: Hostname must not end with a trailing slash")
	}

	// Infos
	fmt.Printf("Listening on: %s\n", listenAddr)
	fmt.Printf("Mirroring requests to: %s\n", mirrorhost)
	fmt.Printf("Worker pool: %d workers, queue-size=%d, queue-block=%v, GOMAXPROCS=%d\n",
		workers, queueSize, queueBlock, runtime.GOMAXPROCS(0))

	// Listener
	listener, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		log.Printf("error create listener, %v", err)
		os.Exit(1)
	}
	defer listener.Close()

	// start worker-queue + pool
	jobQueue = make(chan mirrorJob, queueSize)
	startWorkerPool(workers, jobQueue)

	// SPOE-Agent
	a := agent.New(handler, logger.NewDefaultLog())
	if err := a.Serve(listener); err != nil {
		log.Printf("error agent serve: %+v\n", err)
	}

	close(jobQueue)
	workersWg.Wait()
}

// ---------- SPOE handler ----------

func handler(req *request.Request) {
	if debug {
		log.Printf("handle request EngineID: '%s', StreamID: '%d', FrameID: '%d' with %d messages\n", req.EngineID, req.StreamID, req.FrameID, req.Messages.Len())
	}

	const messageName = "mirror"

	mes, err := req.Messages.GetByName(messageName)
	if err != nil {
		// no "mirror" in this event -> ignore
		return
	}

	method, found := mes.KV.Get("arg_method")
	if !found {
		log.Printf("arg_method not found in message")
		return
	}

	path, found := mes.KV.Get("arg_path")
	if !found {
		log.Printf("arg_path not found in message")
		return
	}

	hdrs, found := mes.KV.Get("arg_hdrs")
	if !found {
		log.Printf("arg_hdrs not found in message")
		return
	}

	body, found := mes.KV.Get("arg_body")
	if !found {
		log.Printf("arg_body not found in message")
		return
	}

	methodString := method.(string)
	pathString := path.(string)
	bodyBytes := body.([]byte)

	if verbose {
		log.Printf("SPOA-MIRROR %s %s - %s %s - %s\n", methodString, pathString, mirrorhost, listenAddr, string(bodyBytes))
	}

	var hdrsString string
	switch v := hdrs.(type) {
	case []uint8:
		log.Printf("Invalid header format. In mirror.cnf make sure to use arg_hdrs=req.hdrs instead of arg_hdrs=req.hdrs_bin")
		return
	case string:
		hdrsString = v
	default:
		log.Printf("Unable to decode headers.")
		return
	}

	bodyCopy := make([]byte, len(bodyBytes))
	copy(bodyCopy, bodyBytes)

	enqueue(mirrorJob{
		method:  methodString,
		path:    pathString,
		headers: hdrsString,
		body:    bodyCopy,
	})
}

// ---------- HTTP Mirror Request ----------

func makeHTTPRequest(reqMethod string, reqPath string, reqHeaders string, reqBody []byte) {
	req, err := http.NewRequest(reqMethod, mirrorhost+reqPath, bytes.NewReader(reqBody))
	if err != nil {
		log.Printf("Error creating request: %v\n", err)
		return
	}

	sc := bufio.NewScanner(strings.NewReader(reqHeaders))
	for sc.Scan() {
		header := sc.Text()
		if header == "" {
			continue
		}
		parts := strings.SplitN(header, ": ", 2)
		if len(parts) != 2 {
			if debug {
				log.Printf("Invalid header format: %q", header)
			}
			continue
		}
		key := parts[0]
		value := parts[1]
		req.Header.Set(key, value)
	}
	if err := sc.Err(); err != nil && debug {
		log.Printf("scanner error on headers: %v", err)
	}

	// HTTP Call
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error making HTTP request: %v\n", err)
		return
	}
	defer resp.Body.Close()

	if verbose {
		log.Printf("SPOA-MIRROR HTTP %s %s -> %d", reqMethod, req.URL, resp.StatusCode)
	}
}
