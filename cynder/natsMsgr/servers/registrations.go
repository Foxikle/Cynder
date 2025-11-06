package servers

import (
	"encoding/json"
	"fmt"
	"log"
	"time"

	"github.com/CytonicMC/Cydian/servers/registry"
	"github.com/CytonicMC/Cynder/cynder/env"
	"github.com/CytonicMC/Cynder/cynder/messaging"
	"github.com/CytonicMC/Cynder/cynder/util/servers"
	"github.com/nats-io/nats.go"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

func ListenForServerRegistrations(nc messaging.NatsService, proxy *proxy.Proxy) {
	subject := env.EnsurePrefixed("servers.proxy.startup.notify")

	err := nc.Subscribe(subject, func(msg *nats.Msg) {
		var server registry.ServerInfo
		log.Printf("Received message: %s\n", string(msg.Data))
		if err := json.Unmarshal(msg.Data, &server); err != nil {
			log.Printf("Invalid message format: %s", msg.Data)
			return
		}

		// actually do something with server

		registeredServer, err := proxy.Register(servers.CreateServerInfo(server.IP, server.Port, server.ID))
		if err != nil {
			log.Printf("Failed to register server: %s", msg.Data)
			return
		}
		AddServerToGroup(server.Group, server.Type, registeredServer)
		log.Printf("Registered server: '%s' in group '%s'", server.ID, server.Group)
	})
	if err != nil {
		log.Fatalf("Error subscribing to subject %s: %v", subject, err)
	}
	log.Printf("Listening for server registrations on subject '%s'", subject)
}

func ListenForServerShutdowns(nc messaging.NatsService, proxy *proxy.Proxy) {
	subject := env.EnsurePrefixed("servers.proxy.shutdown.notify")

	err := nc.Subscribe(subject, func(msg *nats.Msg) {
		var server registry.ServerInfo
		if err := json.Unmarshal(msg.Data, &server); err != nil {
			log.Printf("Invalid message format: %s", msg.Data)
			return
		}
		fmt.Printf("Recieved shutdown request for server: '%s' in group '%s'", server.ID, server.Group)

		// actually do something with server
		registeredServer := proxy.Server(server.ID)
		RemoveServerFromGroup(server.Group, server.Type, registeredServer)

		if registeredServer == nil {
			log.Printf("No registered server under ID '%s'", server.ID)
			return
		}

		proxy.Unregister(registeredServer.ServerInfo())
	})
	if err != nil {
		log.Fatalf("Error subscribing to subject %s: %v", subject, err)
	}
	log.Printf("Listening for server shutdowns on subject '%s'", subject)
}

func FetchServers(nc messaging.NatsService, proxy *proxy.Proxy) {
	subject := env.EnsurePrefixed("servers.proxy.startup")
	msg, err := nc.Request(subject, []byte(""), time.Second*30)
	if err != nil {
		log.Fatalf("Error fetching servers: %v", err)
	}

	var parsedServers []registry.ServerInfo
	err2 := json.Unmarshal(msg.Data, &parsedServers)
	if err2 != nil {
		log.Fatalf("Error parsing servers: %v", err2)
	}

	for _, server := range parsedServers {
		registeredServer, err := proxy.Register(servers.CreateServerInfo(server.IP, server.Port, server.ID))
		if err != nil {
			log.Printf("Failed to register server: %s", msg.Data)
			return
		}
		AddServerToGroup(server.Group, server.Type, registeredServer)
		log.Printf("Registered server: '%s' in group '%s' with type '%s' from Cydian!", server.ID, server.Group, server.Type)

	}

}
