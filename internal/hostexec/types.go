package hostexec

// Input is one command: Args[0] is the program; Stdin may hold secrets.
type Input struct {
	Args  []string
	Stdin string
}
