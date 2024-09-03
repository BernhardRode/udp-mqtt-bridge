package main

import (
	"fmt"
	"io"
	"log"
	"os"
	"path/filepath"
	"runtime"
	"time"
	"udp_mqtt_bridge/internal/mqtt"
	"udp_mqtt_bridge/internal/udp"
	"udp_mqtt_bridge/pkg/utils"

	"github.com/eiannone/keyboard"

	"gopkg.in/yaml.v2"
)

const CONFIG_DIRECTORY = "udp-mqtt-bridge"

// Config represents the application configuration.
type Config struct {
	AWSClientId    string `yaml:"awsClientId"`
	AWSIOTCert     string `yaml:"awsIotCert"`
	AWSIOTProtocol string `yaml:"awsIotProtocol"`
	AWSIOTEndpoint string `yaml:"awsIotEndpoint"`
	AWSIOTKey      string `yaml:"awsIotKey"`
	AWSIOTPort     int    `yaml:"awsIotPort"`
	AWSIOTRootCA   string `yaml:"awsIotRootCA"`
	MQTTTopicIn    string `yaml:"mqttTopicIn"`
	MQTTTopicOut   string `yaml:"mqttTopicOut"`
	UDPIpIn        string `yaml:"udpIpIn"`
	UDPIpOut       string `yaml:"udpIpOut"`
	UDPPortIn      int    `yaml:"udpPortIn"`
	UDPPortOut     int    `yaml:"udpPortOut"`
}

// loadConfig loads the configuration from a YAML file.
func loadConfig(filename string) (*Config, error) {
	file, err := os.Open(filename)
	if err != nil {
		return nil, err
	}
	defer file.Close()

	log.Printf("Loading configuration from %s", filename)

	// Reset the file pointer to the beginning
	file.Seek(0, io.SeekStart)

	var config Config
	decoder := yaml.NewDecoder(file)
	if err := decoder.Decode(&config); err != nil {
		return nil, err
	}

	log.Printf("Configuration loaded: %+v", config)

	return &config, nil
}

func configPath() (string, error) {
	currentDir, _ := os.Getwd()
	localConfigPath := filepath.Join(currentDir, "configs")
	if _, err := os.Stat(localConfigPath); err == nil {
		log.Printf("Using local configuration directory: %s", localConfigPath)
		return localConfigPath, nil
	}

	userConfigDir, err := os.UserConfigDir()
	if err != nil {
		return "", err
	}
	userConfigDir = filepath.Join(userConfigDir, CONFIG_DIRECTORY)
	log.Printf("Using user configuration directory: %s", userConfigDir)
	return userConfigDir, nil
}

func main() {
	// Map to store timestamps for CloudEvent IDs
	eventTimestamps := make(map[string]time.Time)

	// Get configuration file path
	configPath, err := configPath()
	if err != nil {
		log.Fatalf("Error getting configuration file path: %v", err)
	}

	// Load configuration (e.g., from config.yaml)
	config, err := loadConfig(filepath.Join(configPath, "config.yaml"))
	if err != nil {
		log.Fatalf("Error loading configuration: %v", err)
	}

	// Initialize UDP and MQTT
	udpConn, err := udp.NewConnection(config.UDPIpIn, config.UDPPortIn)
	if err != nil {
		log.Fatalf("Error initializing UDP: %v", err)
	}
	log.Printf("Listening on UDP port %d", config.UDPPortIn)

	broker := fmt.Sprintf("%s://%s:%d", config.AWSIOTProtocol, config.AWSIOTEndpoint, config.AWSIOTPort)
	mqttClient, err := mqtt.NewClient(broker, config.AWSClientId, config.AWSIOTCert, config.AWSIOTKey, config.AWSIOTRootCA, config.MQTTTopicIn)
	if err != nil {
		log.Fatalf("Error initializing MQTT: %v", err)
	}

	// Start capturing keyboard input
	if err := keyboard.Open(); err != nil {
		log.Fatalf("Error opening keyboard: %v", err)
	}

	log.Printf("")
	log.Println("Press 'space' to send a send a ping.")
	log.Println("Press 'q', 'esc' or 'ctrl+c' to quit.")
	defer keyboard.Close()

	// Main application loop
	go func() {
		for {
			select {
			case udpPacket := <-udpConn.Receive():
				// Unmarshal the UDP packet into a CloudEvent
				ce, err := utils.UnmarshalCloudEvent(udpPacket)
				if err != nil {
					log.Printf("Error unmarshalling CloudEvent via UDP: %v", err)
					continue
				}
				log.Printf("Forwarding CloudEvent from UDP to MQTT: %s %s", ce.ID(), ce.Type())

				mqttClient.Send(config.MQTTTopicOut, ce)

			case mqttMsg := <-mqttClient.Receive():
				log.Printf("Received MQTT message: %s", string(mqttMsg))
				// Unmarshal the UDP packet into a CloudEvent
				ce, err := utils.UnmarshalCloudEvent(mqttMsg)
				if err != nil {
					log.Printf("Error unmarshalling CloudEvent via MQTT: %v", err)
					continue
				}
				log.Printf("Received CloudEvent via MQTT: %s %s", ce.ID(), ce.Type())

				// Calculate and log the duration
				if startTime, ok := eventTimestamps[ce.ID()]; ok {
					duration := time.Since(startTime)
					log.Printf("Duration for CloudEvent ID: %s %s - %v", ce.ID(), ce.Type(), duration)
					// Optionally, remove the entry from the map if no longer needed
					delete(eventTimestamps, ce.ID())
				}

				log.Printf("Forwarding CloudEvent from MQTT to UDP: %s %s", ce.ID(), ce.Type())
				// Process the MQTT message and possibly send it to UDP
				udpConn.Send(config.UDPIpOut, config.UDPPortOut, ce)
			}
		}
	}()

	// Capture keyboard input and send UDP ping message on space bar press
	for {
		char, key, _ := keyboard.GetKey()
		if key == keyboard.KeySpace {
			ce, err := utils.CreateCloudEvent("com.bosch-engineering.ping", "https://bosch-engineering.com", "ping")
			if err != nil {
				return
			}
			log.Println("Space bar pressed, sending ping package via UDP...")

			udpConn.Send(config.UDPIpOut, config.UDPPortOut, ce)
			eventTimestamps[ce.ID()] = time.Now()
		}
		if char == 'q' || key == keyboard.KeyEsc || (key == keyboard.KeyCtrlC && runtime.GOOS != "windows") {
			log.Println("Exiting...")
			break
		}
	}

}
