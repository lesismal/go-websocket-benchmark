package conf

const (
	FastHTTP           = "fasthttp"
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
)

var Ports = map[string]string{
	Gobwas:             "11001:11050",
	Gorilla:            "12001:12050",
	Gws:                "13001:13050",
	GwsBasedonStdhttp:  "14001:14050",
	NbioBasedonStdhttp: "15001:15050",
	NbioModBlocking:    "16001:16050",
	NbioModMixed:       "17001:17050",
	NbioModNonblocking: "18001:18050",
	Nhooyr:             "19001:19050",
	Hertz:              "20001:20050",
	FastHTTP:           "21001:21050",
}

var FrameworkList = []string{
	FastHTTP,
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
}
