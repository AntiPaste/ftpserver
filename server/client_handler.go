package server

import (
	"bufio"
	"errors"
	"fmt"
	"io"
	"net"
	"strings"
	"time"
)

type clientHandler struct {
	Id          uint32               // Id of the client
	daddy       *FtpServer           // Server on which the connection was accepted
	driver      ClientHandlingDriver // Client handling driver
	conn        net.Conn             // TCP connection
	writer      *bufio.Writer        // Writer on the TCP connection
	reader      *bufio.Reader        // Reader on the TCP connection
	user        string               // Authenticated user
	isAuthed    bool                 // Has the user authenticated yet
	path        string               // Current path
	command     string               // Command received on the connection
	param       string               // Param of the FTP command
	connectedAt time.Time            // Date of connection
	ctx_rnfr    string               // Rename from
	ctx_rest    int64                // Restart point
	transfer    transferHandler      // Transfer connection (only passive is implemented at this stage)
	transferTls bool                 // Use TLS for transfer connection
}

func (server *FtpServer) NewClientHandler(connection net.Conn) *clientHandler {

	server.clientCounter += 1

	p := &clientHandler{
		daddy:       server,
		conn:        connection,
		Id:          server.clientCounter,
		writer:      bufio.NewWriter(connection),
		reader:      bufio.NewReader(connection),
		connectedAt: time.Now().UTC(),
		path:        "/",
	}

	// Just respecting the existing logic here, this could be probably be dropped at some point

	return p
}

func (c *clientHandler) disconnect() {
	c.conn.Close()
}

func (c *clientHandler) Path() string {
	return c.path
}

func (c *clientHandler) User() string {
	return c.user
}

func (c *clientHandler) SetPath(path string) {
	c.path = path
}

func (c *clientHandler) end() {
	if c.transfer != nil {
		c.transfer.Close()
	}
}

func (c *clientHandler) HandleCommands() {
	defer c.daddy.clientDeparture(c)
	defer c.end()

	if err := c.daddy.clientArrival(c); err != nil {
		c.writeMessage(500, "Can't accept you - "+err.Error())
		return
	}

	defer c.daddy.driver.UserLeft(c)

	//fmt.Println(p.id, " Got client on: ", p.ip)
	if msg, err := c.daddy.driver.WelcomeUser(c); err == nil {
		c.writeMessage(220, msg)
	} else {
		c.writeMessage(500, msg)
		return
	}

	for {
		if c.reader == nil {
			fmt.Fprintln(c.daddy.debugStream, "Clean disconnect")
			return
		}

		line, err := c.reader.ReadString('\n')

		if err != nil {
			if err == io.EOF {
				fmt.Fprintln(c.daddy.debugStream, "TCP disconnect")
			} else {
				fmt.Fprintf(c.daddy.debugStream, "Read error: %s\n", err)
			}

			return
		}

		fmt.Fprintf(c.daddy.debugStream, "FTP RECV: %s\n", line)

		command, param := parseLine(line)
		c.command = strings.ToUpper(command)
		c.param = param

		// If we are doing anything other than authenticating and we have not authenticated
		if c.command != "USER" && c.command != "PASS" && !c.isAuthed {
			c.writeMessage(530, "Please login with USER and PASS")
			continue
		}

		fn := commandsMap[c.command]
		if fn == nil {
			c.writeMessage(550, "Not handled")
		} else {
			fn(c)
		}
	}
}

func (c *clientHandler) writeLine(line string) {
	fmt.Fprintf(c.daddy.debugStream, "FTP SEND: %s\n", line)

	c.writer.Write([]byte(line))
	c.writer.Write([]byte("\r\n"))
	c.writer.Flush()
}

func (c *clientHandler) writeMessage(code int, message string) {
	c.writeLine(fmt.Sprintf("%d %s", code, message))
}

func (c *clientHandler) TransferOpen() (net.Conn, error) {
	if c.transfer != nil {
		c.writeMessage(150, "Using transfer connection")
		conn, err := c.transfer.Open()
		if err == nil {
			fmt.Fprintf(c.daddy.debugStream, "FTP Transfer connection opened to %s\n", conn.RemoteAddr().String())
		}
		return conn, err
	} else {
		c.writeMessage(550, "No passive connection declared")
		return nil, errors.New("No passive connection declared")
	}
}

func (c *clientHandler) TransferClose() {
	if c.transfer != nil {
		c.writeMessage(226, "Closing transfer connection")
		c.transfer.Close()
		c.transfer = nil

		fmt.Fprintln(c.daddy.debugStream, "FTP Transfer connection closed")
	}
}

func (c *clientHandler) IsTransferClosed() bool {
	return c.transfer == nil
}

func parseLine(line string) (string, string) {
	params := strings.SplitN(strings.Trim(line, "\r\n"), " ", 2)
	if len(params) == 1 {
		return params[0], ""
	}
	return params[0], strings.TrimSpace(params[1])
}
