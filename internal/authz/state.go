package authz

import "context"

type BindingState string

const (
	StateUnbound BindingState = "unbound"
	StateBound   BindingState = "bound"
	StateChanged BindingState = "changed"
	StateRevoked BindingState = "revoked"
)

type BindingTransition struct {
	Actor string
	From  BindingState
	To    BindingState
}

func TransitionBinding(ctx context.Context, binding RoleBinding, transition BindingTransition) (RoleBinding, error) {
	if err := ctx.Err(); err != nil {
		return RoleBinding{}, err
	}
	if err := binding.Validate(); err != nil {
		return RoleBinding{}, err
	}
	current := binding.State
	if current == "" {
		current = StateUnbound
	}
	if current != transition.From {
		return RoleBinding{}, ErrConflict
	}
	if transition.Actor == "" || transition.Actor != binding.BoundBy {
		return RoleBinding{}, ErrDenied
	}
	if !validBindingEdge(transition.From, transition.To) {
		return RoleBinding{}, ErrConflict
	}
	binding.State = transition.To
	return binding, nil
}

func validBindingEdge(from, to BindingState) bool {
	return (from == StateUnbound && to == StateBound) ||
		(from == StateBound && (to == StateChanged || to == StateRevoked)) ||
		(from == StateChanged && to == StateRevoked)
}
