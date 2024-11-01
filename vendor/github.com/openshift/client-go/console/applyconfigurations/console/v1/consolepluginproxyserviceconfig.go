// Code generated by applyconfiguration-gen. DO NOT EDIT.

package v1

// ConsolePluginProxyServiceConfigApplyConfiguration represents a declarative configuration of the ConsolePluginProxyServiceConfig type for use
// with apply.
type ConsolePluginProxyServiceConfigApplyConfiguration struct {
	Name      *string `json:"name,omitempty"`
	Namespace *string `json:"namespace,omitempty"`
	Port      *int32  `json:"port,omitempty"`
}

// ConsolePluginProxyServiceConfigApplyConfiguration constructs a declarative configuration of the ConsolePluginProxyServiceConfig type for use with
// apply.
func ConsolePluginProxyServiceConfig() *ConsolePluginProxyServiceConfigApplyConfiguration {
	return &ConsolePluginProxyServiceConfigApplyConfiguration{}
}

// WithName sets the Name field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Name field is set to the value of the last call.
func (b *ConsolePluginProxyServiceConfigApplyConfiguration) WithName(value string) *ConsolePluginProxyServiceConfigApplyConfiguration {
	b.Name = &value
	return b
}

// WithNamespace sets the Namespace field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Namespace field is set to the value of the last call.
func (b *ConsolePluginProxyServiceConfigApplyConfiguration) WithNamespace(value string) *ConsolePluginProxyServiceConfigApplyConfiguration {
	b.Namespace = &value
	return b
}

// WithPort sets the Port field in the declarative configuration to the given value
// and returns the receiver, so that objects can be built by chaining "With" function invocations.
// If called multiple times, the Port field is set to the value of the last call.
func (b *ConsolePluginProxyServiceConfigApplyConfiguration) WithPort(value int32) *ConsolePluginProxyServiceConfigApplyConfiguration {
	b.Port = &value
	return b
}
