package cynder

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"

	"github.com/CytonicMC/Cynder/cynder/env"
	"github.com/CytonicMC/Cynder/cynder/messaging"
	"github.com/CytonicMC/Cynder/cynder/natsMsgr/players"
	"github.com/CytonicMC/Cynder/cynder/natsMsgr/servers"
	"github.com/CytonicMC/Cynder/cynder/redis"
	"github.com/CytonicMC/Cynder/cynder/util"
	"github.com/CytonicMC/Cynder/cynder/util/mini"
	"github.com/go-logr/logr"
	"github.com/nats-io/nats.go"
	redis2 "github.com/redis/go-redis/v9"
	"github.com/robinbraemer/event"
	"go.minekube.com/common/minecraft/color"
	"go.minekube.com/common/minecraft/component"
	"go.minekube.com/gate/pkg/edition/java/proxy"
)

type Dependencies struct {
	NatsConn    *nats.Conn
	Logger      logr.Logger
	RedisClient *redis2.Client
}

var (
	Instance *Cynder
)

// Plugin initialization using dependencies
var Plugin = proxy.Plugin{
	Name: "CynderInit",
	Init: func(ctx context.Context, p *proxy.Proxy) error {
		deps, err := InitializeDependencies(ctx)
		if err != nil {
			return err
		}

		ns := &messaging.NatsServiceImpl{
			Conn: deps.NatsConn,
		}

		rs := &redis.ServiceImpl{
			Client: deps.RedisClient,
		}

		Instance = &Cynder{
			Proxy:        p,
			dependencies: deps,
			Services: &util.Services{
				Nats:  ns,
				Redis: rs,
			},
		}

		// player stuff
		players.HandlePlayerSend(Instance.Services, p, ctx)
		players.HandleGenericSend(Instance.Services, p, ctx)
		players.HandlePlayerKick(Instance.Services, p, ctx)

		// servers
		servers.FetchServers(ns, p)
		servers.ListenForServerRegistrations(ns, p)
		servers.ListenForServerShutdowns(ns, p)

		registerEvents(p, ns, deps.Logger, rs)

		return nil
	},
}

func InitializeDependencies(ctx context.Context) (*Dependencies, error) {
	return &Dependencies{
		NatsConn:    ConnectToNats(),
		Logger:      logr.FromContextOrDiscard(ctx).WithName("Cynder"),
		RedisClient: redis.ConnectToRedis(),
	}, nil
}

type Cynder struct {
	dependencies *Dependencies
	Proxy        *proxy.Proxy
	Services     *util.Services
}

// when joining for the first time, the player is always sent to a lobby
func registerEvents(p *proxy.Proxy, nc messaging.NatsService, logger logr.Logger, rc redis.Service) {

	event.Subscribe(p.Event(), 0, func(e *proxy.PlayerChooseInitialServerEvent) {
		server := servers.GetLeastLoadedServer("cytonic", "lobby")
		//server := servers.GetLeastLoadedServer("gilded_gorge", "hub")

		if server == nil {
			e.Player().Disconnect(mini.Parse("<color:red><bold>WHOOPS!</bold></color:red><color:gray> There are no lobby servers to connect to right now. Try again later!"))
			return
		}
		e.SetInitialServer(server)
	})

	event.Subscribe(p.Event(), 0, func(e *proxy.PreLoginEvent) {
		id, _ := e.ID()

		banned, banMessage := util.IsBanned(id, rc)
		if banned {
			e.Deny(banMessage)
			return
		}

		if env.IsRestricted() {
			if !util.CanJoinRestrictedServer(id, rc) {
				e.Deny(mini.Parse("<color:red><bold>WHOOPS!</bold></color:red><color:gray> You are not allowed to join this server!<newline> Contact an administrator for a whitelist if you believe this is an error."))
				return
			}
		}

		players.BroadcastPlayerJoin(nc, e.Username(), id, logger)
		rc.SetHashPrefixed("online_players", id.String(), e.Username())
	})

	event.Subscribe(p.Event(), 0, func(e *proxy.DisconnectEvent) {
		id := e.Player().ID()
		players.BroadcastPlayerLeave(nc, e.Player().Username(), id, logger)
		rc.RemHashPrefixed("online_players", id.String())
		rc.RemHashPrefixed("player_servers", id.String())
		rc.RemHashPrefixed("cytosis:nicknames", id.String())
	})

	event.Subscribe(p.Event(), 100, func(e *proxy.ServerPostConnectEvent) {

		var oldServer string
		if e.PreviousServer() != nil {
			oldServer = e.PreviousServer().ServerInfo().Name()
		}

		container := &messaging.PlayerChangeServerContainer{
			Player:    e.Player().ID(),
			OldServer: oldServer,
			NewServer: e.Player().CurrentServer().Server().ServerInfo().Name(),
		}

		rc.SetHashPrefixed("player_servers", e.Player().ID().String(), container.NewServer)

		data, err := json.Marshal(container)
		if err != nil {
			logger.Error(err, "Failed to marshal PlayerChangeServerContainer")
			return
		}

		er1 := nc.Publish("players.server_change.notify", data)
		if er1 != nil {
			logger.Error(err, "Failed broadcast player server change!")
			return
		}
	})

	event.Subscribe(p.Event(), 100, func(e *proxy.KickedFromServerEvent) {

		server, success := servers.GetFallbackFromServer(e.Server(), e.Server().ServerInfo().Name())

		if !success || server == nil || server.ServerInfo() == e.Server().ServerInfo() {
			msg := mini.Parse("<color:red><bold>WHOOPS!</bold></color:red><color:gray> Failed to rescue from internal disconnect. Initial kick reason: ")
			e.SetResult(&proxy.DisconnectPlayerKickResult{
				Reason: &component.Text{
					Content: "",
					S:       component.Style{},
					Extra: []component.Component{
						msg,
						e.OriginalReason(),
					},
				},
			})
			logger.Info("Failed to rescue from internal disconnect. ")
		} else {
			logger.Info("Successfully rescued from internal disconnect. ")
			reason := e.OriginalReason()
			if reason != nil {
				reason.Style().Color = color.Red
			}
			comp := &component.Text{
				Content: "",
				S:       component.Style{},
				Extra: []component.Component{
					mini.Parse("<color:gold><bold>YOINK!</bold></color:gold><color:gray> A kick occurred in your connection, so you were placed in a lobby!"),
					mini.Parse("<color:red> ("),
					e.OriginalReason(),
					mini.Parse("<color:red>)"),
				},
			}

			e.SetResult(&proxy.RedirectPlayerKickResult{
				Server:  server,
				Message: comp,
			})
		}
	})
}

func ConnectToNats() *nats.Conn {

	// Connect to natsMsgr server
	username := os.Getenv("NATS_USERNAME")
	password := os.Getenv("NATS_PASSWORD")
	hostname := os.Getenv("NATS_HOSTNAME")
	port := os.Getenv("NATS_PORT")

	url := fmt.Sprintf("nats://%s:%s@%s:%s", username, password, hostname, port)
	nc, err := nats.Connect(url)
	if err != nil {
		log.Fatalf("Error connecting to nats: %v \n\nURL: %s", err, url)
	}
	//defer nc.Close()
	log.Println("Connected to nats!")

	return nc
}
