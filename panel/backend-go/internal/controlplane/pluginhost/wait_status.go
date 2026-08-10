package pluginhost

import "errors"

type expectedHostTerminationWaitError struct {
	err       error
	interrupt bool
	kill      bool
}

func (e *expectedHostTerminationWaitError) Error() string { return e.err.Error() }
func (e *expectedHostTerminationWaitError) Unwrap() error { return e.err }

func normalizeExpectedTerminationWaitError(err error, interruptAccepted, killAccepted bool) error {
	if err == nil || (!interruptAccepted && !killAccepted) {
		return err
	}
	var classified *expectedHostTerminationWaitError
	if errors.As(err, &classified) && ((classified.interrupt && interruptAccepted) || (classified.kill && killAccepted)) {
		return nil
	}
	if platformExpectedTerminationWaitError(err, interruptAccepted, killAccepted) {
		return nil
	}
	return err
}
