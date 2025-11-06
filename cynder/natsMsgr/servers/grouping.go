package servers

import (
	"fmt"

	"go.minekube.com/gate/pkg/edition/java/proxy"
)

// First fallback this way ->
// then V
var fallbackHierarchy = map[string][]string{
	"gilded_gorge": {"instancing_server", "hub"},
	"bedwars":      {"quads", "duos", "solos", "lobby"},
	"cytonic":      {"lobby"},
}

// empty by default
var currentServers = map[string]map[string][]proxy.RegisteredServer{}

// Structure: group -> type -> []servers
// ie: gilded_gorge -> instancing_server -> servers

func AddServerToGroup(group string, serverType string, server proxy.RegisteredServer) {
	// Ensure the group exists
	if _, exists := currentServers[group]; !exists {
		currentServers[group] = make(map[string][]proxy.RegisteredServer)
	}
	// Add server to the specific type
	currentServers[group][serverType] = append(currentServers[group][serverType], server)
	fmt.Printf("Added server %s to group %s (type: %s)\n", server.ServerInfo().Name(), group, serverType)
}

func RemoveServerFromGroup(group string, serverType string, server proxy.RegisteredServer) {
	if serversByType, exists := currentServers[group]; exists {
		if servers, exists := serversByType[serverType]; exists {
			for i, s := range servers {
				if s == nil || s.ServerInfo() == nil || server == nil || server.ServerInfo() == nil {
					return
				}
				if s.ServerInfo().Name() == server.ServerInfo().Name() {
					// Remove the server from the slice
					currentServers[group][serverType] = append(servers[:i], servers[i+1:]...)
					fmt.Printf("Removed server %s from group %s (type: %s)\n", server.ServerInfo().Name(), group, serverType)
					break
				}
			}
		}
	}
}

// todo: get fallbacks in layers. (ie in some random gg server, send to personal gorge, then if that fails to lobby)
func GetLeastLoadedServer(group string, serverType string, excludeIds ...string) proxy.RegisteredServer {
	fmt.Printf("Fetching least loaded server for group %s with type %s\n", group, serverType)

	if _, exists := currentServers[group]; !exists {
		return nil // Group doesn't exist
	}
	if _, exists := currentServers[group][serverType]; !exists {
		return nil // Type doesn't exist in this group
	}

	var leastLoadedServer proxy.RegisteredServer
	leastLoad := int(^uint(0) >> 1) // Max possible int

	for _, server := range currentServers[group][serverType] {
		if contains(excludeIds, server.ServerInfo().Name()) {
			continue
		}

		var b string
		if leastLoadedServer != nil {
			b = leastLoadedServer.ServerInfo().Name()
		} else {
			b = "NONE"
		}

		fmt.Printf("Server %s has %d players! Least load is: %d, which is server %s\n", server.ServerInfo().Name(), server.Players().Len(), leastLoad, b)
		if server.Players().Len() < leastLoad {
			leastLoad = server.Players().Len()
			leastLoadedServer = server
		}
	}

	return leastLoadedServer
}

func GetFallbackServer(currentGroup string, serverType string, excludeIds ...string) (proxy.RegisteredServer, bool) {
	// Find the next fallback group and type
	nextGroup, nextType, found := GetNextFallbackGroup(currentGroup, serverType)
	if !found {
		// Check if the group has any fallback types available
		if len(fallbackHierarchy[currentGroup]) == 0 {
			// If no fallback found and no fallback types exist for this group, return failure
			fmt.Printf("No fallback types available for group %s\n", currentGroup)
			return nil, false
		}

		// If no fallback found, use the "bottom type" of the current group
		nextGroup = currentGroup
		nextType = fallbackHierarchy[currentGroup][len(fallbackHierarchy[currentGroup])-1]
	}

	// Try to get the least loaded server from the next group and type
	server := GetLeastLoadedServer(nextGroup, nextType, excludeIds...)
	if server != nil {
		return server, true
	}

	// If the fallback type still doesn't have a server, recursively try the next fallback group
	// BUT only if we actually found a valid next fallback to prevent infinite recursion
	if found {
		return GetFallbackServer(nextGroup, nextType, excludeIds...)
	}

	// If we've exhausted all fallbacks, return failure
	fmt.Printf("No servers available in any fallback for group %s, type %s\n", currentGroup, serverType)
	return nil, false
}

func GetNextFallbackGroup(currentGroup string, typeToStartFrom string) (string, string, bool) {
	groupKeys := make([]string, 0, len(fallbackHierarchy))
	for key := range fallbackHierarchy {
		groupKeys = append(groupKeys, key)
	}

	// Find the starting group and type
	for groupIndex, group := range groupKeys {
		if group == currentGroup {
			fallbacks := fallbackHierarchy[group]

			// Find the type in the current group and move rightward
			for i, fallback := range fallbacks {
				if fallback == typeToStartFrom {
					// Move to the next fallback in the same group
					if i+1 < len(fallbacks) {
						return group, fallbacks[i+1], true
					}
					// Move to the next group in the hierarchy, if available
					for j := groupIndex + 1; j < len(groupKeys); j++ {
						nextGroup := groupKeys[j]
						if len(fallbackHierarchy[nextGroup]) > 0 {
							return nextGroup, fallbackHierarchy[nextGroup][0], true
						}
					}
					fmt.Printf("No further fallbacks available for group %s and type %s\n", currentGroup, typeToStartFrom)
					return "", "", false // No further fallbacks
				}
			}
		}
	}

	return "", "", false // Not found
}

func GetFallbackFromServer(currentServer proxy.RegisteredServer, excludeIds ...string) (proxy.RegisteredServer, bool) {
	var currentGroup, serverType string

	// Add current server to exclusion list
	excludeIds = append(excludeIds, currentServer.ServerInfo().Name())

	// Find the group and type of the current server
	for group, types := range currentServers {
		for typ, servers := range types {
			for _, server := range servers {
				if server.ServerInfo().Name() == currentServer.ServerInfo().Name() {
					currentGroup = group
					serverType = typ
					break
				}
			}
			if currentGroup != "" {
				break
			}
		}
		if currentGroup != "" {
			break
		}
	}

	// If we found the server's group and type, try normal fallback
	if currentGroup != "" && serverType != "" {
		fallbackServer, found := GetFallbackServer(currentGroup, serverType, excludeIds...)
		if found {
			return fallbackServer, true
		}
	}

	// If normal fallback failed or server wasn't found in groups, try alternative approaches
	fmt.Printf("Server %s not found in any group or normal fallback failed, attempting alternative fallback\n", currentServer.ServerInfo().Name())

	// Try fallback within the same group if we know the group
	if currentGroup != "" && len(fallbackHierarchy[currentGroup]) > 0 {
		// Try the last type in the current group's hierarchy (typically a lobby or hub)
		lastType := fallbackHierarchy[currentGroup][len(fallbackHierarchy[currentGroup])-1]
		fallbackServer := GetLeastLoadedServer(currentGroup, lastType, excludeIds...)
		if fallbackServer != nil {
			fmt.Printf("Fallback server found in same group: %s\n", fallbackServer.ServerInfo().Name())
			return fallbackServer, true
		}
	}

	// Try other groups in fallback hierarchy order
	groupKeys := make([]string, 0, len(fallbackHierarchy))
	for key := range fallbackHierarchy {
		groupKeys = append(groupKeys, key)
	}

	for _, group := range groupKeys {
		if group == currentGroup {
			continue // Skip current group, already tried above
		}
		types := fallbackHierarchy[group]
		if len(types) > 0 {
			// Try the last type in this group (typically the safest fallback like lobby/hub)
			lastType := types[len(types)-1]
			fallbackServer := GetLeastLoadedServer(group, lastType, excludeIds...)
			if fallbackServer != nil {
				fmt.Printf("Fallback server found in different group %s: %s\n", group, fallbackServer.ServerInfo().Name())
				return fallbackServer, true
			}
		}
	}

	// Last resort: try any available server from any group/type that's not excluded
	for group, types := range currentServers {
		for typ, servers := range types {
			for _, server := range servers {
				if server == nil || server.ServerInfo() == nil {
					continue
				}
				if !contains(excludeIds, server.ServerInfo().Name()) {
					fmt.Printf("Last resort fallback server found: %s (group: %s, type: %s)\n",
						server.ServerInfo().Name(), group, typ)
					return server, true
				}
			}
		}
	}

	fmt.Printf("No fallback found for server %s after checking all options\n", currentServer.ServerInfo().Name())
	return nil, false
}

// contains checks if a slice contains a specific element
func contains(slice []string, item string) bool {
	for _, v := range slice {
		if v == item {
			return true
		}
	}
	return false
}
