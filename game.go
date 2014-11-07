package mess

import (
	"fmt"
	"log"
	"strings"
	"time"
)

type ThingId int
type ThingIdList []ThingId
type ThingType int

const (
	RegularThing ThingType = iota
	PlaceThing
	PlayerThing
	ActionThing
	ProgramThing
)

func ThingTypeForName(name string) ThingType {
	switch name {
	case "place":
		return PlaceThing
	case "player":
		return PlayerThing
	case "action":
		return ActionThing
	case "program":
		return ProgramThing
	}
	return RegularThing
}

func (tt ThingType) String() string {
	switch tt {
	case PlaceThing:
		return "place"
	case PlayerThing:
		return "player"
	case ActionThing:
		return "action"
	case ProgramThing:
		return "program"
	}
	return "thing"
}

func (tl *ThingIdList) Things() []*Thing {
	things := make([]*Thing, len(*tl))
	for i := 0; i < len(*tl); i++ {
		things[i] = World.ThingForId((*tl)[i])
	}
	return things
}

type Thing struct {
	Id      ThingId
	Type    ThingType
	Name    string
	Parent  ThingId
	Creator ThingId
	Created time.Time

	Owner     ThingId
	AdminList ThingIdList
	AllowList ThingIdList
	DenyList  ThingIdList

	Contents ThingIdList
	Table    map[string]interface{}

	Client *ClientPump
}

func NewThing() (thing *Thing) {
	thing = &Thing{
		Contents: make([]ThingId, 0),
		Table:    make(map[string]interface{}),
	}
	return
}

func (thing *Thing) GetURL() string {
	return fmt.Sprintf("/%s/%d/", thing.Type.String(), thing.Id)
}

func (thing *Thing) GetParent() *Thing {
	return World.ThingForId(thing.Parent)
}

func (thing *Thing) GetOwner() *Thing {
	return World.ThingForId(thing.Owner)
}

func (thing *Thing) GetContents() (contents []*Thing) {
	for _, thingId := range thing.Contents {
		content := World.ThingForId(thingId)
		if content.Type != ActionThing {
			contents = append(contents, content)
		}
	}
	return
}

func (thing *Thing) GetActions() (actions []*Thing) {
	for _, thingId := range thing.Contents {
		action := World.ThingForId(thingId)
		if action.Type == ActionThing {
			actions = append(actions, action)
		}
	}
	return
}

func (thing *Thing) ActionMatches(command string) bool {
	if thing.Type != ActionThing {
		return false
	}

	if strings.ToLower(thing.Name) == command {
		log.Println("Found action", thing, "has name", command, "!")
		return true
	}

	// Check our aliases.
	if aliases, ok := thing.Table["aliases"]; ok {
		if aliasesList, ok := aliases.([]interface{}); ok {
			for _, alias := range aliasesList {
				if aliasStr, ok := alias.(string); ok {
					if strings.ToLower(aliasStr) == command {
						log.Println("Found action", thing, "has alias", command, "!")
						return true
					}
				}
			}
		} else {
			log.Println("Found action", thing, "but could not cast its aliases list to a []string; skipping")
		}
	} else {
		log.Println("Found action", thing, "but it has no aliases; skipping")
	}

	return false
}

func (thing *Thing) ActionTarget() (target *Thing) {
	if targetId, ok := thing.Table["target"]; ok {
		// JSON numbers are float64s. :|
		if targetIdNum, ok := targetId.(float64); ok {
			targetIdInt := ThingId(targetIdNum)
			target = World.ThingForId(targetIdInt)
		}
	}
	return
}

var World WorldStore
var Accounts AccountStore

func GameInit() {
	db, err := OpenDatabase()
	if err != nil {
		log.Println("Error connecting to database:", err)
		return
	}

	World = &ActiveWorld{
		Things: make(map[ThingId]*Thing),
		Next:   db,
	}
	Accounts = db
}

func Identify(source *Thing, name string) *Thing {
	nameLower := strings.ToLower(name)
	if nameLower == "me" {
		return source
	}

	here := World.ThingForId(source.Parent)
	if nameLower == "here" {
		return here
	}

	for _, otherId := range here.Contents {
		otherThing := World.ThingForId(otherId)
		otherNameLower := strings.ToLower(otherThing.Name)
		if strings.HasPrefix(otherNameLower, nameLower) {
			return otherThing
		}
	}

	return nil
}

func GameLook(client *ClientPump, char *Thing, rest string) {
	if rest == "" {
		rest = "here"
	}
	target := Identify(char, rest)

	if target != nil {
		client.ToClient <- target.Name
		desc, ok := target.Table["description"].(string)
		if ok && desc != "" {
			client.ToClient <- desc
		} else {
			client.ToClient <- "You see nothing special."
		}
		return
	}

	client.ToClient <- fmt.Sprintf("Not sure what you meant by \"%s\".", rest)
}

func GameSay(client *ClientPump, char *Thing, rest string) {
	client.ToClient <- fmt.Sprintf("You say, \"%s\"", rest)

	text := fmt.Sprintf("%s says, \"%s\"", char.Name, rest)
	parent := World.ThingForId(char.Parent)
	for _, otherId := range parent.Contents {
		if otherId == char.Id {
			continue
		}
		otherChar := World.ThingForId(otherId)
		if otherChar.Client != nil {
			otherChar.Client.ToClient <- text
		}
	}
}

func GameClient(client *ClientPump, account *Account) {
	char := World.ThingForId(account.Character)
	if char.Client != nil {
		// TODO: kill the old one???
	}
	char.Client = client

	// We just arrived from the welcome screen, so "look" around.
	// TODO: motd?
	GameLook(client, char, "")

	for input := range client.ToServer {
		parts := strings.SplitN(input, " ", 2)
		command := strings.ToLower(parts[0])
		rest := ""
		if len(parts) > 1 {
			rest = parts[1]
		}
		log.Println("Unused portion of command:", rest)

	Command:
		switch command {
		case "quit":
			client.ToClient <- "Thanks for spending time with the mess today!"
			close(client.ToClient)
			break
		case "look":
			GameLook(client, char, rest)
		case "say":
			GameSay(client, char, rest)
		default:
			// Look up the environment for an action with that command.
			var action *Thing
			thisThing := char
		FindThing:
			for thisThing != nil {
				for _, thingId := range thisThing.Contents {
					thing := World.ThingForId(thingId)
					if thing.ActionMatches(command) {
						action = thing
						break FindThing
					}
				}

				// No actions on thisThing matched. Try up the environment.
				thisThing = World.ThingForId(thisThing.Parent)
			}
			if action != nil {
				target := action.ActionTarget()
				// TODO: run a program if target.Type == ProgramThing
				// TODO: move to target.Parent if PlayerThing or RegularThing
				if target != nil && target.Type == PlaceThing {
					World.MoveThing(char, target)

					// We moved so let's have a new look shall we.
					GameLook(client, char, "")

					break Command
				}
			}

			// Didn't find such an action.
			client.ToClient <- fmt.Sprintf("Oops, not sure what you mean by \"%s\".", command)
		}
	}
}