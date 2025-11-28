
## About
A collection of simple party games, packed in a single modular webapp.

Feature requests, code criticism, bug reports, general chit-chat, and unrelated angst accepted at `partybox@seedno.de`.

Static binary builds available [here](https://cdn.seedno.de/builds/partybox).

x86_64 and ARM Docker images of latest version: `oci.seedno.de/seednode/partybox:latest`.

Dockerfile available [here](https://github.com/Seednode/partybox/blob/master/docker/Dockerfile).

### Configuration
The following configuration methods are accepted, in order of highest to lowest priority:
- Command-line flags
- Environment variables

### Environment variables
Almost all options configurable via flags can also be configured via environment variables.

The associated environment variable is the prefix `PARTYBOX` plus the flag name, with the following changes:
- Leading hyphens removed
- Converted to upper-case
- All internal hyphens converted to underscores

For example:
```
TZ=America/Chicago
```

## Usage output
Alternatively, you can configure the service using command-line flags.
```
A collection of simple party games, packed in a single modular webapp.

Usage:
  partybox... [flags]

Flags:
  -b, --bind string       address to bind to (env: PARTYBOX_BIND) (default "0.0.0.0")
  -d, --debug             log file permission errors instead of skipping (env: PARTYBOX_DEBUG)
  -h, --help              help for partybox...
  -p, --port int          port to listen on (env: PARTYBOX_PORT) (default 8080)
      --profile           register net/http/pprof handlers (env: PARTYBOX_PROFILE)
      --tls-cert string   path to tls certificate (env: PARTYBOX_TLS_CERT)
      --tls-key string    path to tls keyfile (env: PARTYBOX_TLS_KEY)
  -v, --verbose           display additional output (env: PARTYBOX_VERBOSE)
  -V, --version           display version and exit (env: PARTYBOX_VERSION)
```

## Building the Docker image
From inside the cloned repository, build the image using the following command:

`REGISTRY=<registry url> LATEST=yes TAG=alpine ./build-docker.sh`