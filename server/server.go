// Package server provides all the tools to build your own FTP server: The core library and the driver.
package server

import (
	"fmt"
	"io"
	"io/ioutil"
	"net"
	"sync"
	"time"
)

var commandsMap map[string]func(*clientHandler)

func init() {
	// This is shared between FtpServer instances as there's no point in making the FTP commands behave differently
	// between them.

	commandsMap = make(map[string]func(*clientHandler))

	// Authentication
	commandsMap["USER"] = (*clientHandler).handleUSER
	commandsMap["PASS"] = (*clientHandler).handlePASS

	// File access
	commandsMap["SIZE"] = (*clientHandler).handleSIZE
	commandsMap["MDTM"] = (*clientHandler).handleMDTM
	commandsMap["RETR"] = (*clientHandler).handleRETR
	commandsMap["STOR"] = (*clientHandler).handleSTOR
	commandsMap["APPE"] = (*clientHandler).handleAPPE
	commandsMap["DELE"] = (*clientHandler).handleDELE
	commandsMap["RNFR"] = (*clientHandler).handleRNFR
	commandsMap["RNTO"] = (*clientHandler).handleRNTO
	commandsMap["ALLO"] = (*clientHandler).handleALLO
	commandsMap["REST"] = (*clientHandler).handleREST

	// Directory handling
	commandsMap["CWD"] = (*clientHandler).handleCWD
	commandsMap["PWD"] = (*clientHandler).handlePWD
	commandsMap["CDUP"] = (*clientHandler).handleCDUP
	commandsMap["NLST"] = (*clientHandler).handleLIST
	commandsMap["LIST"] = (*clientHandler).handleLIST
	commandsMap["MKD"] = (*clientHandler).handleMKD
	commandsMap["RMD"] = (*clientHandler).handleRMD

	// Connection handling
	commandsMap["TYPE"] = (*clientHandler).handleTYPE
	commandsMap["PASV"] = (*clientHandler).handlePASV
	commandsMap["EPSV"] = (*clientHandler).handlePASV
	commandsMap["QUIT"] = (*clientHandler).handleQUIT

	// TLS handling
	commandsMap["AUTH"] = (*clientHandler).handleAUTH
	commandsMap["PROT"] = (*clientHandler).handlePROT
	commandsMap["PBSZ"] = (*clientHandler).handlePBSZ

	// Misc
	commandsMap["FEAT"] = (*clientHandler).handleFEAT
	commandsMap["SYST"] = (*clientHandler).handleSYST
	commandsMap["NOOP"] = (*clientHandler).handleNOOP
	commandsMap["OPTS"] = (*clientHandler).handleOPTS
}

type FtpServer struct {
	Settings         *Settings                 // General settings
	Listener         net.Listener              // Listener used to receive files
	StartTime        time.Time                 // Time when the server was started
	connectionsById  map[uint32]*clientHandler // Connections map
	connectionsMutex sync.RWMutex              // Connections map sync
	clientCounter    uint32                    // Clients counter
	driver           ServerDriver              // Driver to handle the client authentication and the file access driver selection
	debugStream      io.Writer
}

func (server *FtpServer) loadSettings() {
	s := server.driver.GetSettings()
	if s.ListenHost == "" {
		s.ListenHost = "0.0.0.0"
	}
	if s.ListenPort == 0 {
		s.ListenPort = 2121
	}
	if s.MaxConnections == 0 {
		s.MaxConnections = 10000
	}
	server.Settings = s
}

func (server *FtpServer) ListenAndServe() error {
	server.loadSettings()
	var err error
	fmt.Fprintln(server.debugStream, "Starting...")

	server.Listener, err = net.Listen(
		"tcp",
		fmt.Sprintf("%s:%d", server.Settings.ListenHost, server.Settings.ListenPort),
	)

	if err != nil {
		return err
	}

	fmt.Fprintf(server.debugStream, "Listening at %s\n", server.Listener.Addr())

	for {
		connection, err := server.Listener.Accept()
		if err != nil {
			fmt.Fprintf(server.debugStream, "Failed to accept connection: %s\n", err)
			return err
		}

		c := server.NewClientHandler(connection)
		go c.HandleCommands()
	}
}

func NewFtpServer(driver ServerDriver) *FtpServer {
	return &FtpServer{
		driver:          driver,
		debugStream:     ioutil.Discard,
		StartTime:       time.Now().UTC(), // Might make sense to put it in Start method
		connectionsById: make(map[uint32]*clientHandler),
	}
}

func (server *FtpServer) SetDebugStream(stream io.Writer) {
	server.debugStream = stream
}

func (server *FtpServer) Stop() {
	for {
		server.connectionsMutex.Lock()

		allClosed := true
		for _, c := range server.connectionsById {
			if !c.IsTransferClosed() {
				allClosed = false
				break
			}
		}

		server.connectionsMutex.Unlock()

		if allClosed {
			break
		}

		time.Sleep(1 * time.Second)
	}

	server.Listener.Close()
}

// When a client connects, the server could refuse the connection
func (server *FtpServer) clientArrival(c *clientHandler) error {
	server.connectionsMutex.Lock()
	defer server.connectionsMutex.Unlock()

	server.connectionsById[c.Id] = c
	nb := len(server.connectionsById)

	fmt.Fprintf(server.debugStream, "Client connected from %s\n", c.conn.RemoteAddr())

	if nb > server.Settings.MaxConnections {
		return fmt.Errorf("Too many clients %d > %d", nb, server.Settings.MaxConnections)
	}

	return nil
}

// When a client leaves
func (server *FtpServer) clientDeparture(c *clientHandler) {
	server.connectionsMutex.Lock()
	defer server.connectionsMutex.Unlock()

	delete(server.connectionsById, c.Id)

	fmt.Fprintf(server.debugStream, "Client from %s disconnected\n", c.conn.RemoteAddr())
}
