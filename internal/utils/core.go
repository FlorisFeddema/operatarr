package utils

import "sync"

func RunConcurrently(fnList ...func() error) chan error {
	errors := make(chan error)
	wg := sync.WaitGroup{}

	// Run all the functions concurrently
	for _, fn := range fnList {
		fn := fn
		wg.Add(1)
		go func() {
			defer wg.Done()
			errors <- fn()
		}()
	}

	// Close the output channel whenever all of the functions completed
	go func() {
		wg.Wait()
		close(errors)
	}()

	// Read from the channel and aggregate into a slice
	return errors
}

// ChannelToSlice consumes a channel return values in a slice
func ChannelToSlice[T any](c chan T) []T {
	list := []T{}
	for value := range c {
		list = append(list, value)
	}
	return list
}
