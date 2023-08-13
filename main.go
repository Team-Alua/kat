package main

import (
	"io"
	"os"
	"os/signal"
	"syscall"
	"log"
	"strconv"
	"fmt"
	"strings"
	"net/http"
	"time"
	"path"
	"path/filepath"
	"github.com/bwmarrin/discordgo"
)

// Variables used for command line parameters
var (
	httpClient = getHttpClient()
	Token string
)

type ClientRequest struct {
	Session *discordgo.Session
	Message *discordgo.MessageCreate
}

func NewClientRequest(s *discordgo.Session, m *discordgo.MessageCreate) ClientRequest {
	return ClientRequest{Session: s, Message: m}
}

type ClientState struct {
	Id string
	Status string
	FileUploads []string
}

func NewClientState(id string) ClientState {
	return ClientState{Id: id, Status: "", FileUploads: make([]string, 10)}
}

var requests chan ClientRequest

func getHttpClient() *http.Client {
	return &http.Client{
		Timeout: 30 * time.Second,
	}
}

func init() {
	data, err := os.ReadFile("token")
	if err != nil {
		panic(err)
	}
	Token = strings.TrimSpace(string(data))
}

func CommandHandler(req <-chan ClientRequest, resp chan<- string) {
	var cs ClientState
	for {
		cr := <- req
		s := cr.Session
		m := cr.Message
		clientId := m.Author.ID
		cs.Status = "End"
		if cs.Status == "" {
			cs.FileUploads = make([]string, 0)
			// So the user can start uploading before
			// the message is sent
			resp <- clientId
			s.ChannelMessageSend(m.ChannelID, "Please upload all the files or link to files. Send done when you are done.")
		} else if cs.Status == "FileUpload" {
			// Try to do this as quickly as possible so the user doesn't have to wait.
			if m.Content == "Done" {
				cs.Status = "Online"
				resp <- clientId
				s.ChannelMessageSend(m.ChannelID, "Ready to apply online patches?")
			} else {
				for _, attachment := range m.Attachments {
					cs.FileUploads = append(cs.FileUploads, attachment.URL)
				}
				resp <- clientId
			}
		} else if cs.Status == "Online" {
			cs.Status = "End"
			resp <- clientId
			s.ChannelMessageSend(m.ChannelID, "End")
		} else if cs.Status == "End" {
			s.ChannelMessageSend(m.ChannelID, "This is the end")
			resp <- ("end:" + clientId)
			break
		}
	}	
	fmt.Println("Exiting goroutine")
}

func RequestHandler(ch <-chan ClientRequest) {
	respChan := make(chan string)
	states := make(map[string]chan ClientRequest)
	queue := make(map[string][]ClientRequest)
	active := make(map[string]bool)
	for {
		select {
		case cr := <-ch:
			var reqChan chan ClientRequest
			id := cr.Message.Author.ID
			// Check if user has a state or not
			if _, ok := states[id]; !ok {
				states[id] = make(chan ClientRequest)
			}
			// Get the ClientRequest channel
			reqChan = states[id]

			if _, ok := queue[id]; !ok {
				queue[id] = make([]ClientRequest, 0)
			}
			_, ok := active[id]
			fmt.Println("Is ok?", ok)

			// Give user their own goroutine
			if _, ok := active[id]; !ok {
				active[id] = true
				go CommandHandler(states[id], respChan)
				reqChan <- cr
			} else if active[id] {
				// Add to user queue
				queue[id] = append(queue[id], cr)
			} else {
				// Send user request
				reqChan <- cr
			}
		case id := <-respChan:
			if strings.HasPrefix(id, "end:") {
				id = id[len("end:"):]
				fmt.Println(id, len(id))
				delete(active, id)
				continue
			}
			uq := queue[id]
			// It's only active if
			// we are adding new messages
			// from the queue
			active[id] = len(uq) > 0
			if len(uq) > 0 {
				reqChan := states[id]
				cr := uq[0]
				queue[id] = append(uq[1:])
				reqChan <- cr
			}
		}
	}

}
func StartRequestListener() {
	requests = make(chan ClientRequest)

	go RequestHandler(requests)
}

func main() {

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
				s.ChannelMessageSend(m.ChannelID, "Woken up")
			} else {
				m.ChannelID = threadChannel.ID
				s.ChannelMessageSend(m.ChannelID, "Woken up again")
			}

		}
		return
	}
	// Every message sent will go to another goroutine that will handle
	// the stateful ness of this
	requests <- NewClientRequest(s,m)
	
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

// https://golangcode.com/download-a-file-from-a-url/
// DownloadFile will download a url to a local file. It's efficient because it will
// write as it downloads and not load the whole file into memory.
func DownloadFile(filepath string, url string) error {

	// Get the data
	resp, err := http.Get(url)
	if err != nil {
		return err
	}

	defer resp.Body.Close()

	// Create the file
	out, err := os.Create(filepath)
	if err != nil {
		return err
	}
	defer out.Close()

	// Write the body to file
	_, err = io.Copy(out, resp.Body)
	return err
}

// https://github.com/NyanKiyoshi/deletednt-discord
func processAttachment(attachment *discordgo.MessageAttachment) {
	// Get the attachment using a HEAD request and ensure it succeed: no error and HTTP 200
	if response, err := httpClient.Head(attachment.URL); err != nil {
		log.Fatalf("failed to get %s: %s", attachment.URL, err)
	} else if response.StatusCode != 200 {
		log.Fatalf(
			"failed to get %s, got response code: %d",
			attachment.URL, response.StatusCode)
	} else {
		log.Printf("successfully pre-fetched %s", attachment.URL)
		url, file := path.Split(attachment.URL)
		ext := filepath.Ext(file)
		if (ext == ".zip" || ext == ".rar" || ext == ".7z" || ext == ".zstd") {
			log.Printf("base url: %s", url)
			log.Printf("file: %s",  file)
			log.Printf("extension: %s",  ext)
			err := DownloadFile(file, attachment.URL)
			if err != nil {
				log.Fatalf("Error retrving file to local disk! %s", err)
			}
				log.Printf("Downloaded: " + attachment.URL)
		} else {
			log.Printf("Mismatched file extension! \"%s\" Ignoring download.", ext)
		}
	}
}

func processAttachments(attachments []*discordgo.MessageAttachment) {
	for _, attachment := range attachments {
		processAttachment(attachment)
	}
}

