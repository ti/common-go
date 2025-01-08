package database

import "fmt"

// Transaction a Transaction interface
type Transaction interface {
	Rollback() error
	Commit() error
}

// E element the kv value
type E struct {
	Key   string
	Value any
}

// D the documents
type D []E

// CE the condition elements
type CE struct {
	Key   string
	Value any
	C     Condition
}

// C the conditions
type C []CE

// String print the condition as string
func (c C) String() (result string) {
	for _, v := range c {
		result += fmt.Sprintf("[%s %v %v]", v.Key, v.C, v.Value)
	}
	return
}

// Condition the condition
type Condition uint8

// Condition
const (
	// Eq =
	Eq Condition = iota
	// Ne !=
	Ne
	// Lt <
	Lt
	// Lte <=
	Lte
	// Gt >
	Gt
	// Gte >=
	Gte
	// In [a,b,c]
	In
	// Nin Not in [a,b,c]
	Nin
)

// BulkError error for bulk update
type BulkError struct {
	Elements []*BulkElement
	Err      error
}

// Error the implement for error
func (e *BulkError) Error() string {
	return e.Err.Error()
}

// BulkElement bulk element for insert
type BulkElement struct {
	Index   int
	Message string
}
