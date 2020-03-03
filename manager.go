package graphql

import (
	"fmt"

	"github.com/GannettDigital/graphql/gqlerrors"
)

const (
	requestQueueBuffer = 10 // this also defines the number of permanent workers which is double this number
)

// resolveRequest contains the information needed to resolve a field.
// A resolveRequest is passed to a resolveManager worker which processes the request.
type resolveRequest struct {
	fn       FieldResolveFn
	name     string
	params   ResolveParams
	response chan<- resolverResponse
}

// resolverResponse is the type containing the resolver response which is sent over a channel as workers finish
// processing.
type resolverResponse struct {
	err    error
	name   string
	result interface{}
}

// resolveManager runs resolve functions and completeValue requests with a set of worker go routines.
// Having a set of workers limits the churn of go routines while still providing parallel resolving of results
// which is key to performance when some resolving requires a network call.
//
// The nature of the GraphQL resolving code is that a single resolve call could end up calling other resolve calls
// as part of it. This means to avoid a full deadlock a hard limit on the number of workers can't be set, instead
// a slight buffer and a slight delay is added to give preference to reusing workers over creating new.
//
// A small set of workers are long lived to allow some processing to happen at all times and enable a buffered channel
// Most other workers are not long lived but will only exit when the request channels are empty.
type resolveManager struct {
	resolveRequests chan resolveRequest
}

func newResolveManager() *resolveManager {
	manager := &resolveManager{
		resolveRequests: make(chan resolveRequest, requestQueueBuffer),
	}

	for i := 0; i < 2*requestQueueBuffer; i++ {
		go manager.infiniteWorker()
	}
	return manager
}

func (manager *resolveManager) infiniteWorker() {
	for {
		select {
		case req := <-manager.resolveRequests:
			manager.resolve(req)
		}
	}
}

func (manager *resolveManager) newWorker() {
	for {
		select {
		case req := <-manager.resolveRequests:
			manager.resolve(req)
		default:
			return
		}
	}
}

func (manager *resolveManager) resolve(req resolveRequest) {
	defer func() {
		if r := recover(); r != nil {
			var err error
			if r, ok := r.(string); ok {
				err = NewLocatedError(
					fmt.Sprintf("%v", r),
					FieldASTsToNodeASTs(req.params.Info.FieldASTs),
				)
			}
			if r, ok := r.(error); ok {
				err = gqlerrors.FormatError(r)
			}
			req.response <- resolverResponse{name: req.name, err: err}
		}
	}()

	result, err := req.fn(req.params)
	req.response <- resolverResponse{name: req.name, result: result, err: err}
}

func (manager *resolveManager) resolveRequest(name string, response chan<- resolverResponse, fn FieldResolveFn, params ResolveParams) {
	req := resolveRequest{
		fn:       fn,
		name:     name,
		params:   params,
		response: response,
	}

	select {
	case manager.resolveRequests <- req:
		return
	default:
		go manager.newWorker()
		manager.resolveRequests <- req
	}
}
