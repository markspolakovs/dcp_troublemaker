# Couchbase Troublemaker

A Couchbase binary protocol proxy that can rewrite packets using JavaScript.

## Installation

You need Go 1.18 or later to build.

Check out the source code:

```shell
$ git clone https://github.com/markspolakovs/dcp_troublemaker.git
```

And build it:

```shell
$ go build -o troublemaker
```

## Usage
```shell
$ ./troublemaker --help
usage: troublemaker [<flags>] <script-path>

Flags:
--help                      Show context-sensitive help (also try --help-long and --help-man).
--backend-host="127.0.0.1"  backend host
--backend-port=11210        backend port
--listen-port=11210         port to listen on
--log-level="info"          log level
--log-pretty                pretty logging

Args:
<script-path>  path to js
```

## Scripting

The troublemaker accepts scripts written in JavaScript.
The scripts run in a ES5-like environment (though many globals you may expect from a browser will likely be missing.)

The script should expose top-level functions named after memcached binary protocol packet types (eg `CMD_GET`, `CMD_DCPOPENSTREAM`).
For a full list of names, see the [gocbcore source](https://github.com/couchbase/gocbcore/blob/master/memd/cmdcode.go).

The function should take two parameters:
* an object with the fields of the incoming packet
* and a boolean which is `false` when the packet came from the client to the server and `true` if it came from the server to the client

The script has access to a number of global functions:
* `forward(packet)`: forwards the packet to its original destination
* `reply(packet)`: sends the given packet back to the originator
* `log(string)`: logs to standard output

In addition, the script can modify the packet object (or even create new ones on the fly).
It has access to all the fields of the gocbcore [`memd.Packet` struct](https://github.com/couchbase/gocbcore/blob/master/memd/packet.go).
