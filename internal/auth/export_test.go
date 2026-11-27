package auth

// FreeMailSlots is a test hook: how many of the package's mail send slots are free.
func FreeMailSlots() int { return cap(mailSlots) - len(mailSlots) }
