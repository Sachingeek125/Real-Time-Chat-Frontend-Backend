package main

import (
	"context"
	"encoding/json"
	"fmt"
	"log"
	"os"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/gofiber/fiber/v2"
	"github.com/gofiber/template/html/v2"
	"github.com/gofiber/websocket/v2"
	"github.com/jackc/pgx/v4/pgxpool"
	"github.com/lib/pq"
)

// struct for private messages
type Message struct {
	Sender         string    `json:"sender"`
	Receiver       string    `json:"receiver"`
	MessageContent string    `json:"message_content"`
	SentTime       time.Time `json:"sent_time"`
}

// struct for group details with group name and group members
type Group_Details struct {
	Group_Name    string   `json:"group_name"`
	Group_Members []string `json:"group_members"`
}

// struct for group messages with group name, sender, message and sent time
type Group_Msg struct {
	Group_Name     string    `json:"group_name"`
	Sender         string    `json:"sender"`
	MessageContent string    `json:"message_content"`
	SentTime       time.Time `json:"sent_time"`
}

// struct for loading prev messages of particular group
type Load_Msg struct {
	Sender         string `json:"sender"`
	MessageContent string `json:"message_content"`
}

// Define a structure to store group information
type Group struct {
	Name     string   // Group name
	Members  []string // Members of the group
	Messages []string // Messages sent to the group
}

// Create a map to store groups using group names as keys
var groups = make(map[string]*Group)

// struct for client information for websocket
type Client struct {
	conn     *websocket.Conn
	username string
}

// struct for user's information
type Users struct {
	User_ID   string
	User_Name string
}

var (
	clients     = make(map[string]*Client)
	clientsLock sync.Mutex
)

func main() {
	// Create a new HTML template engine
	engine := html.New("./views", ".html")
	app := fiber.New(fiber.Config{
		Views: engine,
	})

	// connecting to a postgres via connection string
	connString := "postgres://postgres:root@localhost:5432/chat"
	pool, err := pgxpool.Connect(context.Background(), connString)
	if err != nil {
		log.Fatal(err)
	}

	// api endpoint for homepage
	app.Get("/", func(c *fiber.Ctx) error {
		// select all the usernames from database
		rows, err := pool.Query(context.Background(), "SELECT user_name FROM users")
		if err != nil {
			log.Println("Error fetching users:", err)
			return err
		}
		// closes the rows
		defer rows.Close()

		// storing all user's username into users
		var users []string
		for rows.Next() {
			var username string
			err := rows.Scan(&username)
			if err != nil {
				log.Println("Error scanning users:", err)
				return err
			}
			users = append(users, username)
		}

		// Convert the users slice to a JSON string
		usersJSON, err := json.Marshal(users)
		fmt.Println(string(usersJSON))
		if err != nil {
			log.Println("Error marshaling users:", err)
			return err
		}

		// Pass the JSON string to the HTML template
		return c.Render("index", fiber.Map{
			"Users": string(usersJSON),
		})
	})

	app.Post("/", func(c *fiber.Ctx) error {
		// Retrieve the "name" parameter from the form data in the request.
		name := c.FormValue("name")

		// Query to check if the name exists in the users table
		query := "SELECT EXISTS(SELECT 1 FROM users WHERE user_name = $1)"

		var exists bool
		if err := pool.QueryRow(context.Background(), query, name).Scan(&exists); err != nil {
			fmt.Fprintf(os.Stderr, "Error executing query: %v\n", err)
			return c.Status(fiber.StatusInternalServerError).SendString("Database error")
		}

		// Fetch the list of usernames from the database
		rows, err := pool.Query(context.Background(), "SELECT user_name FROM users")
		if err != nil {
			log.Println("Error fetching users:", err)
			return err
		}
		defer rows.Close()

		//adding all of username into users
		var users []string
		for rows.Next() {
			var username string
			err := rows.Scan(&username)
			if err != nil {
				log.Println("Error scanning users:", err)
				return err
			}
			users = append(users, username)
		}

		// if user has entered duplicate name then doesn't need to store it in database just render the page with name and userslist passed into it
		if exists {
			return c.Render("ws5", fiber.Map{
				"name":  name,
				"users": users,
			})
		}

		// if user entered unique name then stores in users and then render the page with name and userslist passed into it
		_, err = pool.Exec(context.Background(), `
			INSERT INTO users(user_name)
			VALUES ($1)
		`, name)

		if err != nil {
			return err
		}

		return c.Render("ws5", fiber.Map{
			"name":  name,
			"users": users,
		})
	})

	// Route for suggestions based upon user's input in the form for every input
	app.Get("/suggestions", func(c *fiber.Ctx) error {
		// Retrive name parameter from query
		name := c.Query("name")

		// Query to fetch suggestions from the users table based on the input name
		query := "SELECT user_name FROM users WHERE user_name LIKE $1"

		// search for a input name pattern in a users table.
		rows, err := pool.Query(context.Background(), query, name+"%")

		if err != nil {
			log.Println("Error fetching suggestions:", err)
			return err
		}
		defer rows.Close()

		// storing all of the fetched results for suggestion
		var suggestions []string
		for rows.Next() {
			var username string
			err := rows.Scan(&username)
			if err != nil {
				log.Println("Error scanning suggestions:", err)
				return err
			}
			suggestions = append(suggestions, username)
		}

		// sending json data to the client side for showing suggestions
		return c.JSON(fiber.Map{
			"suggestions": suggestions,
		})
	})

	// route to fetch the grouplist for specific user
	app.Get("/fetch-group-list", func(c *fiber.Ctx) error {
		// fetch user from query parameter
		name := c.Query("user")

		// fetch the list of groups where any of the members is current user
		query := "SELECT group_name FROM group_details WHERE $1=ANY(group_members)"
		rows, err := pool.Query(context.Background(), query, name)
		// if error occured during the execution of query then informs the client side
		if err != nil {
			log.Println("Error fetching Groups:", err)
			return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
				"error":   "Error fetching groups",
				"details": err.Error(), // Include error details in the response
			})
		}
		// closes the rows
		defer rows.Close()

		// stores the all fetched grouplist into grps[]string with error handling for errors during the scanning of fetched groups
		var grps []string
		for rows.Next() {
			var grp string
			err := rows.Scan(&grp)
			if err != nil {
				log.Println("Error scanning users:", err)
				return c.Status(fiber.StatusInternalServerError).JSON(fiber.Map{
					"error":   "Error scanning users",
					"details": err.Error(), // Include error details in the response
				})
			}
			grps = append(grps, grp)
		}
		// sending the message to client side if fetched grouplist has 0 length for specific users
		if len(grps) < 1 {
			return c.Status(fiber.StatusFound).JSON(fiber.Map{
				"message": "User's not part of any group",
			})
		}
		// Return the list of groups as JSON to client side for specific users
		return c.JSON(fiber.Map{"groups": grps})
	})

	// route to fetch the all groups
	app.Get("/get-groups", func(c *fiber.Ctx) error {
		// Fetch the list of all groupnames from the database with error handling
		rows, err := pool.Query(context.Background(), "SELECT group_name FROM group_details")
		if err != nil {
			log.Println("Error fetching groups:", err)
			return c.Status(fiber.StatusInternalServerError).SendString("Error fetching groups")
		}
		// closes the row
		defer rows.Close()

		// stores all the fetched group name into the []string
		var grpsdata []string
		for rows.Next() {
			var grpdata string
			err := rows.Scan(&grpdata)
			if err != nil {
				log.Println("Error scanning users:", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Error scanning users")
			}
			grpsdata = append(grpsdata, grpdata)
		}

		// Return the list of groups as JSON to the client side
		return c.JSON(fiber.Map{"groups": grpsdata})
	})

	// route for fetching all users
	app.Get("/get-users", func(c *fiber.Ctx) error {
		// Fetch the list of usernames from the database
		rows, err := pool.Query(context.Background(), "SELECT user_name FROM users")
		if err != nil {
			log.Println("Error fetching users:", err)
			return c.Status(fiber.StatusInternalServerError).SendString("Error fetching users")
		}
		// closes the rows
		defer rows.Close()
		// stores all the fetched user name into the []string
		var users []string
		for rows.Next() {
			var username string
			err := rows.Scan(&username)
			if err != nil {
				log.Println("Error scanning users:", err)
				return c.Status(fiber.StatusInternalServerError).SendString("Error scanning users")
			}
			users = append(users, username)
		}

		// Return the list of users as JSON to the client side
		return c.JSON(fiber.Map{"users": users})
	})

	// route for loading the messages of private chat between two specific user(example:User-A and User-B)
	app.Get("/load-messages", func(c *fiber.Ctx) error {
		// fetch sender and receiver from query parameter
		sender := c.Query("sender")
		receiver := c.Query("receiver")

		// Query the database to retrieve previous messages between these two users(sender and receiver)
		messages, err := loadMessagesFromDB(pool, sender, receiver)
		if err != nil {
			// Return the error
			return err
		}

		// Return the messages between two users as JSON to the client side
		return c.JSON(messages)

	})

	// route for loading the group messages for a specific group
	app.Get("/load-group-messages", func(c *fiber.Ctx) error {

		// fetch the groupname from parameter(suppose our group name is: Coding then here we get Group:Coding)
		grp := c.Query("group")
		// Trimming the prefix of Group: into groupname
		grp = strings.TrimPrefix(grp, "Group:")

		// Create a context with timeout for the query(eg. 5 seconds)
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()

		// Query the database to retrieve messages for specific group and sort them by sent_time in ascending order with error handling if error occurs
		query := `
        SELECT sender,message_content
        FROM group_msg
        WHERE (group_name= $1)
        ORDER BY sent_time ASC`

		rows, err := pool.Query(ctx, query, grp)
		if err != nil {
			return err
		}
		// closes the rows
		defer rows.Close()

		// stores the fetched group messages into []string with appropriate error handling if occurs
		var messages []Load_Msg
		for rows.Next() {
			var message Load_Msg
			if err := rows.Scan(&message.Sender, &message.MessageContent); err != nil {
				return err
			}
			messages = append(messages, message)
		}

		// return the messages for a specific group to the client side
		return c.JSON(messages)

	})

	// route for Upgrading the connection to WebSocket for specific user to enable real-time chat
	app.Get("/ws", websocket.New(func(c *websocket.Conn) {
		// Parse query parameters to get the user's name
		username := c.Query("name")

		// Create a new client object with the connection and username
		client := &Client{
			conn:     c,
			username: username,
		}

		// Register the client in the global clients map
		clientsLock.Lock()
		clients[username] = client
		clientsLock.Unlock()

		// Defer the client's removal from the clients map when the connection is closed
		defer func() {
			// Unregister the client when the connection is closed
			clientsLock.Lock()
			delete(clients, username)
			clientsLock.Unlock()
			fmt.Println("Client disconnected:", username)
		}()

		// Send a welcome message to the client when he has connected to websocket
		if err := c.WriteMessage(websocket.TextMessage, []byte("Your Name is , "+username+"!")); err != nil {
			fmt.Println("Failed to send welcome message:", err)
			return
		}

		// Main loop for handling incoming messages
		for {
			t, msg, err := c.ReadMessage()
			if err != nil {
				fmt.Println("Client disconnected:", err)
				break
			}

			// Parse the incoming message to extract selected users, message content and isgroup bool(to identify message is for group or individual)
			// Here Example message format: "selectedUsers:Travellinggg;messageContent;true"
			parts := strings.SplitN(string(msg), ";", 3)

			// if length of parts is not 3 then it's invalid format
			if len(parts) != 3 {
				// Invalid message format
				fmt.Println("Invalid")
				continue
			}

			// 1st part will be the users/groups which are selected
			// 2nd part will be message content if(group chat/individual chat) otherwise it will be groupname(for group creation)
			// 3rd part will be isGroup bool to identify the group or individual message
			// Format will be like this for selectedUsers:user1,user2... so here we first trim the selectedUsers: and then spliting the users by ','
			userPart := strings.TrimPrefix(parts[0], "selectedUsers:")
			selectedUsers := strings.Split(userPart, ",")
			messageContent := parts[1]
			isGroup := parts[2]

			// parses the string value of isgroup to convert it into bool
			flag, err := strconv.ParseBool(isGroup)

			// for a group chat(as isGroup flag is True)
			// in else it handles for individual chat
			if flag {

				// if length of selected user is >1 that means its for group creation(because we can't select multiple groups for sending the messages
				// into existing group)

				if len(selectedUsers) > 1 {
					// if groupcreation then the groupname will be message content
					groupname := messageContent

					// storing groupname and members into group[] struct
					group := &Group{
						Name:    groupname,
						Members: selectedUsers,
					}
					// as it's for group creation inserts group details into database
					res, err := pool.Exec(context.Background(), `INSERT INTO group_details(group_name, group_members) VALUES ($1, $2)`, group.Name, group.Members)

					fmt.Println(res)
					if err != nil {
						fmt.Println(err)
					}
					// Create a map to store groups using group names as keys
					groups[messageContent] = group
					fmt.Println(group.Members)

					// sends a welcome message to the all of group members for notifying them as they have been added into specific group
					sendGroupMessage(pool, username, selectedUsers, t, []byte("(This is a server Genrated Message)Welcome to my group: "+messageContent), messageContent)
				} else {
					// for a group chat the selectedusers will be groupname and message content will be the message
					groupname := selectedUsers[0]

					fmt.Printf("GroupName:%s---", groupname)
					msg := messageContent

					fmt.Println(msg)
					// Execute the SQL query to fetch group members for the specified group name with an error handling if occurs
					rows, err := pool.Query(context.Background(), "SELECT group_members FROM group_details WHERE group_name = $1", groupname)
					if err != nil {
						fmt.Println(err)
					}
					// closes the rows
					defer rows.Close()
					// Initialize a string array to store group members
					var groupMembers pq.StringArray
					// Iterate over the rows retrieved from the database query
					for rows.Next() {
						// Initialize a string array to temporarily store group member strings
						var groupMembersString pq.StringArray
						// Scan the current row into the temporary string array
						if err := rows.Scan(&groupMembersString); err != nil {
							fmt.Println(err)
						}
						// Convert the pq.StringArray to a regular []string
						groupMembers = []string(groupMembersString)
						// Output to the console to show the group members
						fmt.Println("Grp_Members")
						fmt.Println(groupMembers)
					}
					//sends that message to all of members of that specific group
					sendGroupMessage(pool, username, groupMembers, t, []byte(msg), groupname)
				}
			} else {
				// Handle private messages(as isGroup flag is false)
				// identify the receipient from selectedusers
				recipient := selectedUsers[1]

				// if sender and receipient is same then inform the sender that you can't send the message to urself
				if recipient == username {
					sendPrivateMessage(pool, username, username, t, []byte("You can't send a private message to yourself"))
				} else {
					// else sends the message to that specific user
					sendPrivateMessage(pool, username, recipient, t, []byte(messageContent))
				}
			}
		}
	}))

	// Start the Fiber app on port 3000 with error handling if occurs
	err = app.Listen(":3000")
	if err != nil {
		log.Fatal("Failed to start server:", err)
	}
}

// loadMessagesFromDB returns fetched messages from the database using pgxpool between two specific users.
func loadMessagesFromDB(pool *pgxpool.Pool, sender, receiver string) ([]Message, error) {
	// Create a context with timeout for the query(eg. 5 seconds)
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()

	// Query the database to retrieve messages between two specific users and sort them by sent_time in ascending order with error handling
	query := `
        SELECT sender, receiver, message_content,sent_time
        FROM messages
        WHERE (sender = $1 AND receiver = $2) OR (sender = $2 AND receiver = $1)
        ORDER BY sent_time ASC`

	rows, err := pool.Query(ctx, query, sender, receiver)
	if err != nil {
		return nil, err
	}
	// closes the rows
	defer rows.Close()

	// stores the messages data of fetched messages from database and returns it
	var messages []Message
	for rows.Next() {
		var message Message
		if err := rows.Scan(&message.Sender, &message.Receiver, &message.MessageContent, &message.SentTime); err != nil {
			return nil, err
		}
		messages = append(messages, message)
	}

	return messages, nil
}

// function for sending group messages from specific member of the specific group to all other members
func sendGroupMessage(pool *pgxpool.Pool, sender string, recipient []string, messageType int, msg []byte, grpname string) {
	// extracting groupname, sender,message from parameters
	grp_message := Group_Msg{
		Group_Name:     grpname,
		Sender:         sender,
		MessageContent: string(msg),
		SentTime:       time.Now(),
	}

	// iterate over recipient list of group for sending the group message to each of them
	for _, r := range recipient {
		// Skip sending the message back to the sender
		if r == sender {
			continue
		}

		// Check if the sender is "Server" and format the message accordingly
		var formattedMessage string
		if sender == "Server" {
			formattedMessage = fmt.Sprintf("you: %s", grp_message.MessageContent)
		} else {
			formattedMessage = fmt.Sprintf("%s:%s", grp_message.Sender, grp_message.MessageContent)
		}

		fmt.Println(formattedMessage)

		// Lock the clientsLock to ensure safe access to the clients map
		clientsLock.Lock()

		// Attempt to find the recipientClient in the clients map using the key 'r'
		recipientClient, found := clients[r]

		// Unlock the clientsLock to allow other goroutines to access the clients map
		clientsLock.Unlock()

		// if the user is not found in clients map then skip writing message to it's connection in order to avoid errors
		if !found {
			continue
		}
		// Send the formatted message to the recipient connection(if found in clients map)
		fmt.Println(formattedMessage)
		err := recipientClient.conn.WriteMessage(messageType, []byte(formattedMessage))
		if err != nil {
			fmt.Println("Error sending private message to client:", err)
		}
	}
	// Insert the message into the group_messages table(for loading prev messages and if offline user joins that chat he can also view messages sent to him)
	_, err := pool.Exec(context.Background(), `
	INSERT INTO group_msg(group_name, sender, message_content, sent_time)
	VALUES ($1, $2, $3, $4)
`, grp_message.Group_Name, grp_message.Sender, grp_message.MessageContent, grp_message.SentTime)

	// error handling if occurs during the insertion of group message
	if err != nil {
		fmt.Println("Error inserting message into the database:", err)
		return
	}
}

// function for sending the messages(1 to one) between two specific users
func sendPrivateMessage(pool *pgxpool.Pool, sender string, recipient string, messageType int, msg []byte) {
	// extracting sender,receiver and message from parameters and storing them into message struct
	message := Message{
		Sender:         sender,
		Receiver:       recipient,
		MessageContent: string(msg),
		SentTime:       time.Now(),
	}

	// Check if this is a broadcast message
	isBroadcast := recipient == "Broadcast"
	// storing the receipient into recipients array of string
	recipients := []string{recipient}

	// If it's a broadcast message, set the recipients to all connected users(so recipients will be all connected users)
	if isBroadcast {
		recipients = []string{}
		clientsLock.Lock()
		for clientName := range clients {
			recipients = append(recipients, clientName)
		}
		clientsLock.Unlock()
	}
	fmt.Println(recipients)

	// now iterate over each recipients and sends them message individually and stores messages into db
	for _, r := range recipients {
		// Skip sending the message back to the sender
		if r == sender {
			continue
		}

		// Check if the sender is "Server" and format the message accordingly
		var formattedMessage string
		if sender == "Server" {
			formattedMessage = fmt.Sprintf("you: %s", message.MessageContent)
		} else {
			formattedMessage = fmt.Sprintf("%s:%s", message.Sender, message.MessageContent)
		}

		fmt.Println(formattedMessage)
		// Insert the message into the messages table(for loading prev messages and if user wants to view all past individual messages with specific other user)
		_, err := pool.Exec(context.Background(), `
			INSERT INTO messages(sender, receiver, message_content, sent_time)
			VALUES ($1, $2, $3, $4)
		`, message.Sender, r, message.MessageContent, message.SentTime)

		if err != nil {
			fmt.Println("Error inserting message into the database:", err)
			return
		}

		// Lock the clientsLock to ensure safe access to the clients map
		clientsLock.Lock()

		// Attempt to find the recipientClient in the clients map using the key 'r'
		recipientClient, found := clients[r]

		// Unlock the clientsLock to allow other goroutines to access the clients map
		clientsLock.Unlock()

		// if the user is not found in clients map then skip writing message to it's connection in order to avoid errors
		if !found {
			continue
		}

		// Send the formatted message to the recipient connection(if found in clients map)
		fmt.Println(formattedMessage)
		err = recipientClient.conn.WriteMessage(messageType, []byte(formattedMessage))
		if err != nil {
			fmt.Println("Error sending private message to client:", err)
		}
	}
}
