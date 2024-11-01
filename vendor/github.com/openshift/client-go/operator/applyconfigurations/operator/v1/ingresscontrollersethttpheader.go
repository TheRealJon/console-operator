// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// IngressControllerSetHTTPHeaderApplyConfiguration represents a declarative configuration of the IngressControllerSetHTTPHeader type for use
// with apply.
type IngressControllerSetHTTPHeaderApplyConfiguration struct {
	Value *string `json:"value,omitempty"`
}

// IngressControllerSetHTTPHeaderApplyConfiguration constructs a declarative configuration of the IngressControllerSetHTTPHeader type for use with
// apply.
func IngressControllerSetHTTPHeader() *IngressControllerSetHTTPHeaderApplyConfiguration {
	return &IngressControllerSetHTTPHeaderApplyConfiguration{}
}

// WithValue sets the Value field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Value field is set to the value of the last call.
func (b *IngressControllerSetHTTPHeaderApplyConfiguration) WithValue(value string) *IngressControllerSetHTTPHeaderApplyConfiguration {
	b.Value = &value
	return b
}
