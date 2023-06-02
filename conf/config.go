package conf

const (
	FasthttpWS         = "fasthttp_ws"
	Gobwas             = "gobwas"
	Gorilla            = "gorilla"
	Gws                = "gws"
	GwsBasedonStdhttp  = "gws_basedon_stdhttp"
	Hertz              = "hertz"
	NbioBasedonStdhttp = "nbio_basedon_stdhttp"
	NbioModBlocking    = "nbio_mod_blocking"
	NbioModMixed       = "nbio_mod_mixed"
	NbioModNonblocking = "nbio_mod_nonblocking"
	Nhooyr             = "nhooyr"
	Nettyws            = "nettyws"
)

var Ports = map[string]string{
	FasthttpWS:         "10001:10050",
	Gobwas:             "11001:11050",
	Gorilla:            "12001:12050",
	Gws:                "13001:13050",
	GwsBasedonStdhttp:  "14001:14050",
	Hertz:              "15001:15050",
	NbioBasedonStdhttp: "16001:16050",
	NbioModBlocking:    "17001:17050",
	NbioModMixed:       "18001:18050",
	NbioModNonblocking: "19001:19050",
	Nhooyr:             "20001:20050",
	Nettyws:            "21001:21050",
}

var FrameworkList = []string{
	FasthttpWS,
	Gobwas,
	Gorilla,
	Gws,
	GwsBasedonStdhttp,
	Hertz,
	NbioBasedonStdhttp,
	NbioModBlocking,
	NbioModMixed,
	NbioModNonblocking,
	Nhooyr,
	Nettyws,
}
