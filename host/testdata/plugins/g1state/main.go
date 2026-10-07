package main

var calls int // module-private global state

func ABIMetadata() string {
	return `{"schema":1,"id":"stateful","version":"1","generation":1,"requires":[],"entries":[{"name":"Count","kind":"call"}]}`
}

func Count(s string) string {
	calls++
	return "g1:" + itoa(calls)
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	var b [20]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func main() {}
