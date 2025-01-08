package uri

const (
	// TagLabel allows to apply a custom behavior.
	// - "allowEmpty" : allows to create an empty struct.
	// - "-": ignore the field.
	TagLabel = "label"

	// TagLabelSliceAsStruct allows to use a slice of struct by creating one entry into the slice.
	// The value is the substitution name used in the label to access the slice.
	TagLabelSliceAsStruct = "label-slice-as-struct"

	// TagLabelAllowEmpty is related to TagLabel.
	TagLabelAllowEmpty = "allowEmpty"
)
