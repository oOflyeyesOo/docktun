package service

import "fmt"

// Workers share the caller's mounts, so they must share its file ownership and
// socket-access groups rather than leaving root-owned update history behind.
func updateWorkerIdentity(c updateContainer) []string {
	args := []string{}
	if user, ok := c.Config["User"].(string); ok && user != "" {
		args = append(args, "--user", user)
	}
	if groups, ok := c.HostConfig["GroupAdd"].([]any); ok {
		for _, group := range groups {
			args = append(args, "--group-add", fmt.Sprint(group))
		}
	}
	if mode, ok := c.HostConfig["UsernsMode"].(string); ok && mode != "" {
		args = append(args, "--userns", mode)
	}
	return args
}
