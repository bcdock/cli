package cli

import "time"

// environment mirrors the fields returned by GET /api/environments and GET /api/environments/{id}.
type environment struct {
	ID                          string     `json:"id"`
	ShortID                     string     `json:"shortId"`
	Name                        string     `json:"name"`
	DisplayName                 string     `json:"displayName"`
	BcVersion                   string     `json:"bcVersion"`
	PlatformVersion             *string    `json:"platformVersion"`
	Country                     string     `json:"country"`
	Location                    string     `json:"location"`
	ArtifactType                string     `json:"artifactType"`
	Status                      string     `json:"status"`
	ProvisioningStage           *string    `json:"provisioningStage"`
	ProvisioningProgressPercent *int       `json:"provisioningProgressPercent"`
	ProvisioningEstimatedMinutes *int      `json:"provisioningEstimatedMinutes"`
	WebClientUrl                *string    `json:"webClientUrl"`
	SoapUrl                     *string    `json:"soapUrl"`
	ODataUrl                    *string    `json:"oDataUrl"`
	DevEndpointUrl              *string    `json:"devEndpointUrl"`
	DownloadsUrl                *string    `json:"downloadsUrl"`
	Username                    *string    `json:"username"`
	Password                    *string    `json:"password"`
	WebServiceAccessKey         *string    `json:"webServiceAccessKey"`
	MultiTenant                 bool       `json:"multiTenant"`
	ErrorMessage                *string    `json:"errorMessage"`
	CreatedAt                   time.Time  `json:"createdAt"`
	CompletedAt                 *time.Time `json:"completedAt"`
	DeletedAt                   *time.Time `json:"deletedAt"`
	SuspendedAt                 *time.Time `json:"suspendedAt"`
	HibernatedAt                *time.Time `json:"hibernatedAt"`
	TotalRunningSeconds         int        `json:"totalRunningSeconds"`
	ContainerStatus             *string    `json:"containerStatus"`
}

// envRow is the compact struct used for table output in env list.
type envRow struct {
	Name    string `header:"NAME"`
	ShortID string `header:"SHORT_ID"`
	Version string `header:"VERSION"`
	Country string `header:"COUNTRY"`
	Status   string `header:"STATUS"`
	Progress string `header:"PROGRESS"`
	Region   string `header:"REGION"`
	Created string `header:"CREATED"`
}

// createEnvRequest is the JSON body for POST /api/environments.
type createEnvRequest struct {
	Name        string `json:"name,omitempty"`
	Version     string `json:"version"`
	Country     string `json:"country"`
	ImageType   string `json:"imageType"`
	Location    string `json:"location,omitempty"`
	MultiTenant bool   `json:"multiTenant"`
}

// createEnvResponse is the 202 Accepted body from POST /api/environments.
type createEnvResponse struct {
	ID      string `json:"id"`
	ShortID string `json:"shortId"`
	Status  string `json:"status"`
}
