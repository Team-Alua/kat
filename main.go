package main

import (
    "os"
    "os/signal"
    "syscall"
    "log"
    "strconv"
    "fmt"
    "strings"
    "github.com/bwmarrin/discordgo"
    "github.com/Team-Alua/kat/discord"
    "github.com/Team-Alua/kat/cmdline"
    "github.com/Team-Alua/kat/userfs"
)

// Variables used for command line parameters
var (
    Token string
)

var requests chan discord.ClientRequest

func init() {
    data, err := os.ReadFile("token")
    if err != nil {
        panic(err)
    }
    Token = strings.TrimSpace(string(data))
}


func RequestHandler(ch <-chan discord.ClientRequest) {
    respChan := make(chan string)
    states := make(map[string]chan discord.ClientRequest)
    active := make(map[string]bool)
    for {
        select {
        case cr := <-ch:
            id := cr.Message.ChannelID + "_" + cr.Message.Author.ID

            if _, ok := states[id]; !ok {
                states[id] = make(chan discord.ClientRequest, 10)
            }

            // Give user their own goroutine
            if _, ok := active[id]; !ok {
                active[id] = false
                // Move all this into a new object
                crc := states[id]
                rc := respChan
                s := cr.Session
                c := cr.Message
                go InterpreterLoop(crc, rc, s, c)
            }
            select {
            case states[id] <- cr:
            default:
                // Do nothing
            }
        case id := <-respChan:
            delete(active, id)
            close(states[id])
            delete(states, id)
        }
    }

}
func StartRequestListener() {
    requests = make(chan discord.ClientRequest)

    go RequestHandler(requests)
}

func main() {
    debug := true
    if debug {
        rw := cmdline.NewReadWriter() 
        mfs, err := userfs.Create("1234")
        if err != nil {
            rw.WriteString(err.Error())
            return
        }
        InterpreterLoopMain("default", mfs, rw)
        return
    }

    // Create a new Discord session using the provided bot token.
    dg, err := discordgo.New("Bot " + Token)
    if err != nil {
        log.Fatalf("error creating Discord session, %s", err)
        return
    }

    dg.AddHandler(messageCreate)
    dg.Identify.Intents = discordgo.IntentsGuildMessages
    dg.Identify.Intents |= discordgo.IntentsGuilds
    // Open a websocket connection to Discord and begin listening.
    err = dg.Open()
    if err != nil {
        log.Fatalf("error opening connection, %s", err)
        return
    }

    StartRequestListener()
    // Restrict access to

    // Wait here until CTRL-C or other term signal is received.
    log.Printf("Bot is now running.")
    sc := make(chan os.Signal, 1)
    signal.Notify(sc, syscall.SIGINT, syscall.SIGTERM, os.Interrupt, os.Kill)
    <-sc
    log.Printf("Killing...")
    log.Printf("Waiting for all remaining ")
    // Cleanly close down the Discord session.
    dg.Close()
}

// This function will be called (due to AddHandler above) every time a new
// message is created on any channel that the authenticated bot has access to.
func messageCreate(s *discordgo.Session, m *discordgo.MessageCreate) {

    if m.Author.ID == s.State.User.ID {
        return
    }
    msg := strings.TrimSpace(m.Content)

    m.Content = msg

    ch, _ := s.State.Channel(m.ChannelID);
    // Check if user is in their thread
    var threadChannel  *discordgo.Channel = nil
    if IsUserThreadChannel(ch, m.Author) {
        threadChannel = ch
        m.ChannelID = threadChannel.ID
    } else if ch.IsThread() {
        // We don't care about this thread
        return
    } else {
        // Check if user already has an active thread
        guild, _ := s.State.Guild(m.GuildID)    
        utn := GetUserThreadChannelName(m.Author)
        
        for _, ch := range guild.Threads {
            if ch.Name == utn {
                // User already has a thread active
                // so ignore new attempts to
                // to create a new channel
                threadChannel = ch
                break
            }
        } 

        // We know the current channel
        // is not the user thread channel
        // We only care about this command
        if msg == "+wakeup" {
            if threadChannel == nil {
                threadChannel = StartBotInteraction(s,m)
                if threadChannel == nil {
                    s.ChannelMessageSend(m.ChannelID, "I could not create a new thread. Please make sure all thread permissions are enabled for me. Heading back to sleep.")
                } else {
                    s.ChannelMessageSend(m.ChannelID, "Woken up")
                }
            } else {
                m.ChannelID = threadChannel.ID
                s.ChannelMessageSend(m.ChannelID, "Woken up again")
            }

        }
        return
    }
    // Every message sent will go to another goroutine that will handle
    // the stateful ness of this
    requests <- discord.NewClientRequest(s,m)
    
}
func GetUserThreadChannelName(user *discordgo.User) string {
    valId, _ := strconv.ParseInt(user.ID, 10, 64)
    return "Save Edit " + user.Username + " " + fmt.Sprintf("%X", valId)
}

func IsUserThreadChannel(ch *discordgo.Channel, user *discordgo.User) bool {
    if !ch.IsThread() {
        return false
    }
    chName := GetUserThreadChannelName(user)
    return ch.Name == chName
}

func StartBotInteraction(s *discordgo.Session , m*discordgo.MessageCreate) *discordgo.Channel {
    thread, err := s.MessageThreadStartComplex(m.ChannelID, m.ID, &discordgo.ThreadStart{
        Name: GetUserThreadChannelName(m.Author),
        AutoArchiveDuration: 60,
        Invitable: false,
        RateLimitPerUser: 5,
    })
    if err != nil {
        return nil
    }
    // The bot will now only talk in this thread
    m.ChannelID = thread.ID
    return thread
}

