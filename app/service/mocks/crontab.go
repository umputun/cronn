// Code generated by moq; DO NOT EDIT.
// github.com/matryer/moq

package mocks

import (
	"context"
	"sync"

	"github.com/umputun/cronn/app/crontab"
)

// CrontabParserMock is a mock implementation of service.CrontabParser.
//
// 	func TestSomethingThatUsesCrontabParser(t *testing.T) {
//
// 		// make and configure a mocked service.CrontabParser
// 		mockedCrontabParser := &CrontabParserMock{
// 			ChangesFunc: func(ctx context.Context) (<-chan []crontab.JobSpec, error) {
// 				panic("mock out the Changes method")
// 			},
// 			ListFunc: func() ([]crontab.JobSpec, error) {
// 				panic("mock out the List method")
// 			},
// 			StringFunc: func() string {
// 				panic("mock out the String method")
// 			},
// 		}
//
// 		// use mockedCrontabParser in code that requires service.CrontabParser
// 		// and then make assertions.
//
// 	}
type CrontabParserMock struct {
	// ChangesFunc mocks the Changes method.
	ChangesFunc func(ctx context.Context) (<-chan []crontab.JobSpec, error)

	// ListFunc mocks the List method.
	ListFunc func() ([]crontab.JobSpec, error)

	// StringFunc mocks the String method.
	StringFunc func() string

	// calls tracks calls to the methods.
	calls struct {
		// Changes holds details about calls to the Changes method.
		Changes []struct {
			// Ctx is the ctx argument value.
			Ctx context.Context
		}
		// List holds details about calls to the List method.
		List []struct {
		}
		// String holds details about calls to the String method.
		String []struct {
		}
	}
	lockChanges sync.RWMutex
	lockList    sync.RWMutex
	lockString  sync.RWMutex
}

// Changes calls ChangesFunc.
func (mock *CrontabParserMock) Changes(ctx context.Context) (<-chan []crontab.JobSpec, error) {
	if mock.ChangesFunc == nil {
		panic("CrontabParserMock.ChangesFunc: method is nil but CrontabParser.Changes was just called")
	}
	callInfo := struct {
		Ctx context.Context
	}{
		Ctx: ctx,
	}
	mock.lockChanges.Lock()
	mock.calls.Changes = append(mock.calls.Changes, callInfo)
	mock.lockChanges.Unlock()
	return mock.ChangesFunc(ctx)
}

// ChangesCalls gets all the calls that were made to Changes.
// Check the length with:
//     len(mockedCrontabParser.ChangesCalls())
func (mock *CrontabParserMock) ChangesCalls() []struct {
	Ctx context.Context
} {
	var calls []struct {
		Ctx context.Context
	}
	mock.lockChanges.RLock()
	calls = mock.calls.Changes
	mock.lockChanges.RUnlock()
	return calls
}

// List calls ListFunc.
func (mock *CrontabParserMock) List() ([]crontab.JobSpec, error) {
	if mock.ListFunc == nil {
		panic("CrontabParserMock.ListFunc: method is nil but CrontabParser.List was just called")
	}
	callInfo := struct {
	}{}
	mock.lockList.Lock()
	mock.calls.List = append(mock.calls.List, callInfo)
	mock.lockList.Unlock()
	return mock.ListFunc()
}

// ListCalls gets all the calls that were made to List.
// Check the length with:
//     len(mockedCrontabParser.ListCalls())
func (mock *CrontabParserMock) ListCalls() []struct {
} {
	var calls []struct {
	}
	mock.lockList.RLock()
	calls = mock.calls.List
	mock.lockList.RUnlock()
	return calls
}

// String calls StringFunc.
func (mock *CrontabParserMock) String() string {
	if mock.StringFunc == nil {
		panic("CrontabParserMock.StringFunc: method is nil but CrontabParser.String was just called")
	}
	callInfo := struct {
	}{}
	mock.lockString.Lock()
	mock.calls.String = append(mock.calls.String, callInfo)
	mock.lockString.Unlock()
	return mock.StringFunc()
}

// StringCalls gets all the calls that were made to String.
// Check the length with:
//     len(mockedCrontabParser.StringCalls())
func (mock *CrontabParserMock) StringCalls() []struct {
} {
	var calls []struct {
	}
	mock.lockString.RLock()
	calls = mock.calls.String
	mock.lockString.RUnlock()
	return calls
}
