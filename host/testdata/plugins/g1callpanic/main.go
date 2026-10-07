package main

func ABIMetadata() string {
	return `{"schema":1,"id":"panicmod","version":"1","generation":1,"requires":[],"entries":[{"name":"Boom","kind":"call"},{"name":"Echo","kind":"call"}]}`
}
func Boom(s string) string { panic("boom in entry") }
func Echo(s string) string { return "ok:" + s }
func main()                {}
