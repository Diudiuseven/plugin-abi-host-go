package main

func ABIMetadata() string {
	return `{"schema":1,"id":"gcap","version":"1","generation":1,"requires":["teleport"],"entries":[{"name":"Echo","kind":"call"}]}`
}
func Echo(s string) string { return s }
func main()                {}
