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
	SchemaVersion int      `json:"SchemaVersion"`
	ID            string   `json:"ID"`
	Hostname      string   `json:"Hostname"`
	Domain        string   `json:"Domain"`
	User          string   `json:"User"`
	Password      string   `json:"Password"`
	Enabled       bool     `json:"Enabled"`
	Systems       []System `json:"Systems,omitempty"`
	Managers      []System `json:"Managers,omitempty"`
}

type System struct {
	ID   string `json:"ID,omitempty"`
	Type string `json:"Type,omitempty"`
	URI  string `json:"URI,omitempty"`
}
