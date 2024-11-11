# HAProxy SPOA Request mirror

This repository contains a program written in golang that utilized the SPOE-Protocol to implement asynchronous request mirroring.

I built this after running into issues with the C-Implementation provided by HAProxy: https://github.com/haproxytech/spoa-mirror

I experienced unexplainable slowdowns for HTTP-Traffic (>100ms extra delay), as well as 100% CPU-Load caused by the spoa-mirror C-Binary, even after the load tests was already stopped. I also encountered random crashes. In my tests I tried to mirror around 3k req/s towards an HTTPS-Endpoint. As my C-Knowledge is not advanced enough to debug this issue, I decided to quickly hack something together in golang.

# Building

You need to have golang installed on wherever you plan to compile the code into a binary.

```
➜  git clone git@github.com:hujiko/spoa-mirror.git
➜  cd spoa-mirror/src
➜  go get
➜  go build -o spoa-mirror main.go
```

This will check out the code from github, install dependencies and compile into a binary called `spoa-mirror`.

# Configuring

## SPOA-Mirror

You need to have `spoa-mirror` running. It accepts two command line arguments:

- listenAddr: A string flag for the listen address, defaulting to "127.0.0.1:12345".
- mirrorHost: A string flag for the hostname where requests should be mirrored. This flag is required and should not end with a trailing slash.

An example to execute the binary would be:

`./spoa-mirror -listen="127.0.0.1:12345" -host="https://destination.example.com"`

## HAProxy

To get an idea on how to configure HAProxy, have a look at the `examples` directory. It contains an example HAProxy config as well as an additional `mirror.cnf` file.
That file needs to be placed on the same instance, that HAProxy is running on. It's location needs to be specified in the HAProxy config file.
