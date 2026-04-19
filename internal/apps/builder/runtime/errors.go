package runtime

// MontyError represents an error from the Monty Python interpreter.
type MontyError struct {
	Message string
}

func (e *MontyError) Error() string {
	return "monty: " + e.Message
}
