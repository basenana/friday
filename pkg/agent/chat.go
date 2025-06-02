package agent

type Option struct {
}

type Reply struct {
	Delta chan string
	Error chan error
}
