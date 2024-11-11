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
	"strings"
	"time"

	"github.com/negasus/haproxy-spoe-go/agent"
	"github.com/negasus/haproxy-spoe-go/logger"
	"github.com/negasus/haproxy-spoe-go/request"
)

// Define httpClient globally so it can reuse connections
var httpClient = &http.Client{
	Timeout: 1 * time.Second, // Set the timeout to 1 second
}
var mirrorhost string
var debug bool

func main() {

	// Define command line flags
	listenAddrParam := flag.String("listen", "127.0.0.1:12345", "Address where the server should listen")
	mirrorhostParam := flag.String("host", "", "Hostname where requests should be mirrored to (no trailing slash)")
	debugParam := flag.Bool("debug", false, "Enable debug mode")

	// Parse the command line flags
	flag.Parse()

	listenAddr := *listenAddrParam
	mirrorhost = *mirrorhostParam
	debug = *debugParam

	// Validate the mirrorHost
	if mirrorhost == "" {
		log.Fatal("Error: Hostname is required")
	}
	if strings.HasSuffix(mirrorhost, "/") {
		log.Fatal("Error: Hostname must not end with a trailing slash")
	}

	// Print the configuration
	fmt.Printf("Listening on: %s\n", listenAddr)
	fmt.Printf("Mirroring requests to: %s\n", mirrorhost)

	listener, err := net.Listen("tcp4", listenAddr)
	if err != nil {
		log.Printf("error create listener, %v", err)
		os.Exit(1)
	}
	defer listener.Close()

	a := agent.New(handler, logger.NewDefaultLog())

	if err := a.Serve(listener); err != nil {
		log.Printf("error agent serve: %+v\n", err)
	}
}

func handler(req *request.Request) {
	if debug {
		log.Printf("handle request EngineID: '%s', StreamID: '%d', FrameID: '%d' with %d messages\n", req.EngineID, req.StreamID, req.FrameID, req.Messages.Len())
	}

	messageName := "mirror"

	mes, err := req.Messages.GetByName(messageName)
	if err != nil {
		log.Printf("message %s not found: %v", messageName, err)
		return
	}

	method, found := mes.KV.Get("arg_method")
	if !found {
		log.Printf("arg_imethod no found in message")
		return
	}

	path, found := mes.KV.Get("arg_path")
	if !found {
		log.Printf("arg_path no found in message")
		return
	}

	hdrs, found := mes.KV.Get("arg_hdrs")
	if !found {
		log.Printf("arg_hdrs no found in message")
		return
	}

	body, found := mes.KV.Get("arg_body")
	if !found {
		log.Printf("arg_body no found in message")
		return
	}

	go makeHTTPRequest(method.(string), path.(string), hdrs.(string), body.([]byte))
}

func makeHTTPRequest(reqMethod string, reqPath string, reqHeaders string, reqBody []byte) {
	// Create a new HTTP request
	req, err := http.NewRequest(reqMethod, mirrorhost+reqPath, bytes.NewReader(reqBody)) // nil for no body
	if err != nil {
		log.Printf("Error creating request: %v\n", err)
		return
	}

	// Get headers
	sc := bufio.NewScanner(strings.NewReader(reqHeaders))
	for sc.Scan() {
		header := sc.Text()

		if header == "" {
			continue
		}

		parts := strings.SplitN(header, ": ", 2) // Split only on the first ": "
		if len(parts) != 2 {
			log.Printf("Invalid header format: %s\n", header)
			continue
		}
		key := parts[0]
		value := parts[1]

		// Set the header
		req.Header.Set(key, value)
	}

	// Make the HTTP request
	resp, err := httpClient.Do(req)
	if err != nil {
		log.Printf("Error making HTTP request: %v\n", err)
		return
	}
	defer resp.Body.Close() // Ensure the response body is closed

}
