package nextcloud

import (
	"encoding/json"
	"encoding/xml"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"strings"
	"time"
)

type LoginResponse struct {
	Poll struct {
		Token    string `json:"token"`
		Endpoint string `json:"endpoint"`
	} `json:"poll"`
	Login string `json:"login"`
}

type PollResponse struct {
	Server   string `json:"server"`
	Username string `json:"loginName"`
	Password string `json:"appPassword"`
}

type FileInfo struct {
	ID           string `json:"id"`
	Name         string `json:"name"`
	Type         string `json:"type"` // "dir" or "file"
	Path         string `json:"path"`
	ETag         string `json:"etag"`
	LastModified int64  `json:"last_modified"`
	Size         int64  `json:"size"`
}

// WebDAV PROPFIND response types
type MultiStatus struct {
	XMLName   xml.Name   `xml:"multistatus"`
	Responses []Response `xml:"response"`
}

type Response struct {
	Href     string   `xml:"href"`
	PropStat PropStat `xml:"propstat"`
}

type PropStat struct {
	Prop   Prop   `xml:"prop"`
	Status string `xml:"status"`
}

type Prop struct {
	DisplayName  string `xml:"displayname"`
	ResourceType struct {
		Collection *struct{} `xml:"collection"`
	} `xml:"resourcetype"`
	FileID           string `xml:"fileid"`
	GetLastModified  string `xml:"getlastmodified"`
	GetETag          string `xml:"getetag"`
	GetContentLength int64  `xml:"getcontentlength"`
}

type Client struct {
	BaseURL  string
	Username string
	Password string
	HTTP     *http.Client
}

func NewClient(url, user, pass string) *Client {
	return &Client{
		BaseURL:  url,
		Username: user,
		Password: pass,
		HTTP: &http.Client{
			Timeout: 30 * time.Second,
		},
	}
}

func (c *Client) TestConnection() error {
	req, err := http.NewRequest("GET", c.BaseURL+"/remote.php/dav/files/"+c.Username+"/", nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK && resp.StatusCode != http.StatusMultiStatus {
		return fmt.Errorf("connection failed with status: %s", resp.Status)
	}

	return nil
}

func (c *Client) ListFiles(path string) ([]FileInfo, error) {
	// Use WebDAV PROPFIND which reliably supports Basic Auth
	endpoint := c.BaseURL + "/remote.php/dav/files/" + url.PathEscape(c.Username) + path

	propfindBody := `<?xml version="1.0" encoding="UTF-8"?>
<d:propfind xmlns:d="DAV:" xmlns:oc="http://owncloud.org/ns" xmlns:nc="http://nextcloud.org/ns">
  <d:prop>
    <d:displayname/>
    <d:resourcetype/>
    <d:getlastmodified/>
    <d:getetag/>
    <d:getcontentlength/>
    <oc:fileid/>
  </d:prop>
</d:propfind>`

	req, err := http.NewRequest("PROPFIND", endpoint, strings.NewReader(propfindBody))
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Content-Type", "application/xml")
	req.Header.Set("Depth", "1")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusMultiStatus && resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("API request failed: %s", resp.Status)
	}

	var ms MultiStatus
	if err := xml.NewDecoder(resp.Body).Decode(&ms); err != nil {
		return nil, err
	}

	var files []FileInfo
	// Normalize the base path for comparison (handle both with and without trailing slash)
	basePath := "/remote.php/dav/files/" + c.Username + path
	basePathNormalized := strings.TrimSuffix(basePath, "/")
	for _, r := range ms.Responses {
		// Skip the directory itself (first response is the requested directory)
		decodedHref, _ := url.PathUnescape(r.Href)
		decodedHrefNormalized := strings.TrimSuffix(decodedHref, "/")
		if decodedHrefNormalized == basePathNormalized {
			continue
		}

		fType := "file"
		if r.PropStat.Prop.ResourceType.Collection != nil {
			fType = "dir"
		}

		name := r.PropStat.Prop.DisplayName
		if name == "" {
			// Extract name from href
			parts := strings.Split(strings.TrimSuffix(decodedHref, "/"), "/")
			if len(parts) > 0 {
				name = parts[len(parts)-1]
			}
		}

		// Parse last modified time
		var lastMod int64
		if r.PropStat.Prop.GetLastModified != "" {
			// Parse HTTP date format
			if t, err := http.ParseTime(r.PropStat.Prop.GetLastModified); err == nil {
				lastMod = t.Unix()
			}
		}

		files = append(files, FileInfo{
			ID:           r.PropStat.Prop.FileID,
			Name:         name,
			Type:         fType,
			Path:         path + "/" + name,
			ETag:         strings.Trim(r.PropStat.Prop.GetETag, `"`),
			LastModified: lastMod,
			Size:         r.PropStat.Prop.GetContentLength,
		})
	}

	return files, nil
}

func InitiateLogin(baseURL string) (*LoginResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", baseURL+"/index.php/login/v2", nil)
	if err != nil {
		return nil, err
	}

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("failed to initiate login: %s", resp.Status)
	}

	var lr LoginResponse
	if err := json.NewDecoder(resp.Body).Decode(&lr); err != nil {
		return nil, err
	}

	return &lr, nil
}

func PollLogin(endpoint, token string) (*PollResponse, error) {
	client := &http.Client{Timeout: 10 * time.Second}
	req, err := http.NewRequest("POST", endpoint, nil)
	if err != nil {
		return nil, err
	}

	q := req.URL.Query()
	q.Add("token", token)
	req.URL.RawQuery = q.Encode()

	resp, err := client.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode == http.StatusNotFound {
		return nil, nil // Still waiting
	}

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("polling failed: %s", resp.Status)
	}

	var pr PollResponse
	if err := json.NewDecoder(resp.Body).Decode(&pr); err != nil {
		return nil, err
	}

	return &pr, nil
}

// UploadFile uploads content to a remote file path
func (c *Client) UploadFile(remotePath string, content []byte) error {
	endpoint := c.BaseURL + "/remote.php/dav/files/" + url.PathEscape(c.Username) + remotePath

	req, err := http.NewRequest("PUT", endpoint, strings.NewReader(string(content)))
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Content-Type", "application/octet-stream")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("upload failed with status: %s", resp.Status)
	}

	return nil
}

// DownloadFile downloads content from a remote file path
func (c *Client) DownloadFile(remotePath string) ([]byte, error) {
	endpoint := c.BaseURL + "/remote.php/dav/files/" + url.PathEscape(c.Username) + remotePath

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("download failed with status: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// DeleteFile deletes a remote file
func (c *Client) DeleteFile(remotePath string) error {
	endpoint := c.BaseURL + "/remote.php/dav/files/" + url.PathEscape(c.Username) + remotePath

	req, err := http.NewRequest("DELETE", endpoint, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusNoContent && resp.StatusCode != http.StatusOK {
		return fmt.Errorf("delete failed with status: %s", resp.Status)
	}

	return nil
}

// MkdirAll creates a directory and all parent directories if they don't exist
func (c *Client) MkdirAll(remotePath string) error {
	endpoint := c.BaseURL + "/remote.php/dav/files/" + url.PathEscape(c.Username) + remotePath

	req, err := http.NewRequest("MKCOL", endpoint, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 201 Created = success, 405 Method Not Allowed = already exists
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusMethodNotAllowed {
		return fmt.Errorf("mkdir failed with status: %s", resp.Status)
	}

	return nil
}

// MoveFile renames/moves a file on the remote server
func (c *Client) MoveFile(oldRemotePath, newRemotePath string) error {
	oldEndpoint := c.BaseURL + "/remote.php/dav/files/" + url.PathEscape(c.Username) + oldRemotePath
	newEndpoint := c.BaseURL + "/remote.php/dav/files/" + url.PathEscape(c.Username) + newRemotePath

	req, err := http.NewRequest("MOVE", oldEndpoint, nil)
	if err != nil {
		return err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("Destination", newEndpoint)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return err
	}
	defer resp.Body.Close()

	// 201 Created = success, 204 No Content = success (overwrite), 412 Precondition Failed = destination exists
	if resp.StatusCode != http.StatusCreated && resp.StatusCode != http.StatusNoContent {
		return fmt.Errorf("move failed with status: %s", resp.Status)
	}

	return nil
}

// GetAvatar fetches the user's avatar image (returns PNG/JPEG bytes)
func (c *Client) GetAvatar(size int) ([]byte, error) {
	endpoint := fmt.Sprintf("%s/index.php/avatar/%s/%d", c.BaseURL, url.PathEscape(c.Username), size)

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return nil, err
	}
	req.SetBasicAuth(c.Username, c.Password)

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return nil, err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return nil, fmt.Errorf("avatar fetch failed with status: %s", resp.Status)
	}

	return io.ReadAll(resp.Body)
}

// GetDisplayName fetches the user's display name from OCS API
func (c *Client) GetDisplayName() (string, error) {
	endpoint := c.BaseURL + "/ocs/v2.php/cloud/user"

	req, err := http.NewRequest("GET", endpoint, nil)
	if err != nil {
		return "", err
	}
	req.SetBasicAuth(c.Username, c.Password)
	req.Header.Set("OCS-APIRequest", "true")
	req.Header.Set("Accept", "application/json")

	resp, err := c.HTTP.Do(req)
	if err != nil {
		return "", err
	}
	defer resp.Body.Close()

	if resp.StatusCode != http.StatusOK {
		return c.Username, nil // Fallback to username
	}

	var result struct {
		Ocs struct {
			Data struct {
				DisplayName string `json:"displayname"`
			} `json:"data"`
		} `json:"ocs"`
	}

	if err := json.NewDecoder(resp.Body).Decode(&result); err != nil {
		return c.Username, nil
	}

	if result.Ocs.Data.DisplayName != "" {
		return result.Ocs.Data.DisplayName, nil
	}
	return c.Username, nil
}
