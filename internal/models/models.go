package models

import "time"

type Device struct {
	Metadata Metadata          `json:"metadata"`
	Spec     DeviceSpecWrapper `json:"spec"`
}

type DeviceSpecWrapper struct {
	DeviceType         string            `json:"deviceType"`
	SerialNumber       string            `json:"serialNumber"`
	PartNumber         string            `json:"partNumber"`
	Manufacturer       string            `json:"manufacturer"`
	ParentID           string            `json:"parentID"`
	ParentSerialNumber string            `json:"parentSerialNumber"`
	Properties         map[string]string `json:"properties"`
}

type Metadata struct {
	UID       string    `json:"uid"`
	UpdatedAt time.Time `json:"updatedAt"`
}

type Candidate struct {
	UID            string
	UpdatedAt      time.Time
	XName          string
	SecretID       string
	RedfishAddress string
	Manufacturer   string
	PartNumber     string
	SerialNumber   string
}

type Credentials struct {
	Username string `json:"username"`
	Password string `json:"password"`
}

type Watermark struct {
	UpdatedAt time.Time `json:"updatedAt"`
	UID       string    `json:"uid"`
}

type SMDRedfishEndpointPayload struct {
	SchemaVersion int       `json:"SchemaVersion"`
	ID            string    `json:"ID"`
	Hostname      string    `json:"Hostname"`
	Domain        string    `json:"Domain"`
	User          string    `json:"User"`
	Password      string    `json:"Password"`
	Enabled       bool      `json:"Enabled"`
	Systems       []System  `json:"Systems,omitempty"`
	Managers      []Manager `json:"Managers,omitempty"`
}

type System struct {
	URI                string              `json:"uri,omitempty"`
	UUID               string              `json:"uuid,omitempty"`
	Manufacturer       string              `json:"manufacturer,omitempty"`
	Model              string              `json:"model,omitempty"`
	Serial             string              `json:"serial,omitempty"`
	BiosVersion        string              `json:"bios_version,omitempty"`
	SystemType         string              `json:"system_type,omitempty"`
	Name               string              `json:"name,omitempty"`
	Actions            []string            `json:"actions,omitempty"`
	ProcessorCount     int                 `json:"processor_count,omitempty"`
	ProcessorType      string              `json:"processor_type,omitempty"`
	MemoryTotal        float32             `json:"memory_total,omitempty"`
	Power              *Power              `json:"power,omitempty"`
	Links              *SystemLinks        `json:"links,omitempty"`
	EthernetInterfaces []EthernetInterface `json:"ethernet_interfaces,omitempty"`
}

type SystemLinks struct {
	Managers []string `json:"managers,omitempty"`
	Chassis  []string `json:"chassis,omitempty"`
}

type EthernetInterface struct {
	URI         string `json:"uri,omitempty"`
	MAC         string `json:"mac,omitempty"`
	IP          string `json:"ip,omitempty"`
	Name        string `json:"name,omitempty"`
	Description string `json:"description,omitempty"`
	Enabled     bool   `json:"enabled,omitempty"`
}

type Power struct {
	State           string   `json:"state,omitempty"`
	PowerControlIDS []string `json:"power_control_ids,omitempty"`
}

type Manager struct {
	URI                string              `json:"uri,omitempty"`
	UUID               string              `json:"uuid,omitempty"`
	Name               string              `json:"name,omitempty"`
	Description        string              `json:"description,omitempty"`
	Model              string              `json:"model,omitempty"`
	ManagerType        string              `json:"type,omitempty"`
	FirmwareVersion    string              `json:"firmware_version,omitempty"`
	EthernetInterfaces []EthernetInterface `json:"ethernet_interfaces,omitempty"`
	Actions            []string            `json:"actions,omitempty"`
	CommandShell       []string            `json:"command_shell,omitempty"`
}
