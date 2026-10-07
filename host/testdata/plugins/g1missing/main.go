package main

func ABIMetadata() string {
	return `{"schema":1,"id":"g1missing","version":"1","generation":1,"requires":[],"entries":[{"name":"Ghost","kind":"call"}]}`
}

// Ghost is declared in the manifest but intentionally not exported.

func main() {}
