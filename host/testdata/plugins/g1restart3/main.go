package main

func ABIMetadata() string {
	return `{"schema":1,"id":"g1restart3","version":"1","generation":1,"requires":["log"],"entries":[{"name":"Echo","kind":"call"}]}`
}
func Echo(s string) string { return "g1restart3:" + s }
func main()                {}
