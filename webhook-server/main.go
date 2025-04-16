package main

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"log/slog"
	"net/http"
	"os"
	"path/filepath"
	"reflect"
	"sort"
	"strconv"
	"strings"

	"github.com/go-ldap/ldap/v3"
	"github.com/mattbaird/jsonpatch"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"

	"github.com/gorilla/mux"
	admissionv1 "k8s.io/api/admission/v1"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

type GiteaAccess struct {
	URL      string
	Username string
	Password string
}

// SecretRef represents a reference to a Kubernetes Secret.
// This is used to specify a Secret from which all key-value pairs
// will be set as environment variables.
type SecretRef struct {
	SecretName string `json:"secretName"`
}

// VolumeMount defines a specific mount point within a container.
// It associates a Volume's Name with a MountPath inside the container,
// indicating where the volume should be mounted.
type VolumeMount struct {
	MountPath string `json:"mountPath"`
	Name      string `json:"name"`
}

// VolumeSource represents the source of a volume to mount.
// It consists of a Name and a Source string. The Source string
// is interpreted to determine the type of volume (like PVC, NFS, etc.).
type VolumeSource struct {
	Name   string `json:"name"`
	Source string `json:"source"`
}

// VolumeConfig encapsulates the configuration for volumes in a Kubernetes environment.
// It includes slices of VolumeMounts and VolumeSources, defining how and where
// different volumes should be mounted in containers.
type VolumeConfig struct {
	VolumeMounts  []VolumeMount  `json:"volumeMounts"`
	VolumeSources []VolumeSource `json:"volumeSources"`
}

// UserProfiles now includes VolumeConfig and a slice of SecretRef
// under the field name SecretsFrom. This allows environment variables
// to be sourced from the specified Kubernetes secrets.
type UserProfiles struct {
	Volumes     VolumeConfig `json:"volumes"`
	SecretsFrom []SecretRef  `json:"secretsFrom"`
}

type PosixGroup struct {
	CN         string   `json:"cn"`
	GIDNumber  string   `json:"gidNumber"`
	MemberUIDs []string `json:"memberUids,omitempty"`
}

// User represents the user profile information
type User struct {
	UID                string       `json:"uid"`
	CommonName         string       `json:"commonName"`
	Surname            string       `json:"surname"`
	GivenName          string       `json:"givenName"`
	DisplayName        string       `json:"displayName"`
	Email              string       `json:"email"`
	Telephone          string       `json:"telephoneNumber"`
	Organization       string       `json:"organization"`
	OrganizationalUnit string       `json:"organizationalUnit"`
	RunAsUser          string       `json:"runAsUser,omitempty"`
	RunAsGroup         string       `json:"runAsGroup,omitempty"`
	FsGroup            string       `json:"fsGroup,omitempty"`
	SupplementalGroups []string     `json:"supplementalGroups,omitempty"`
	Groups             []string     `json:"groups,omitempty"`
	PosixGroups        []PosixGroup `json:"posixGroups,omitempty"` // Added field
	UserAlias          string       `json:"UserAlias,omitempty"`

	// posixAccount fields
	UIDNumber     string `json:"uidNumber,omitempty"`
	GIDNumber     string `json:"gidNumber,omitempty"`
	HomeDirectory string `json:"homeDirectory,omitempty"`
	LoginShell    string `json:"loginShell,omitempty"`
}

// Struct for the main configuration
type Config struct {
	Meta     map[string]string      `json:"meta"`
	Features map[string]interface{} `json:"features"`
	Maps     map[string]string      `json:"maps"`
	Secrets  map[string]string      `json:"secrets"`
}

// Struct for LDAP configuration
type LDAPConfig struct {
	Host                    string `json:"host"`
	Port                    int    `json:"port"`
	Username                string `json:"username"`
	Password                string `json:"-"`
	UserBaseDN              string `json:"user_base_dn"`
	GroupBaseDN             string `json:"group_base_dn"`
	LibNSSLDAPConfigMapName string `json:"libnssLdapConfigMapName"`
}

// AppConfig struct holds paths and loaded configuration
type AppConfig struct {
	ConfigPath  string
	MapsDir     string
	SecretsDir  string
	Config      *Config
	TLSCertPath string
	TLSKeyPath  string
	LDAPConfig  *LDAPConfig
	K8sClient   *kubernetes.Clientset // Add Kubernetes client handle
}

type ProfileResources struct {
	Volumes            []corev1.Volume
	VolumeMounts       []corev1.VolumeMount
	EnvFromSources     []corev1.EnvFromSource
	Env                []corev1.EnvVar
	PodSecurityContext *corev1.PodSecurityContext
	SecurityContext    *corev1.SecurityContext
}

// Global variable to hold application configuration
var appConfig *AppConfig

// Function to load the configuration from a JSON file
func loadConfig(path string) (*Config, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return nil, err
	}

	var config Config
	if err := json.Unmarshal(data, &config); err != nil {
		return nil, err
	}

	return &config, nil
}

// Custom String method to avoid printing the password
func (l LDAPConfig) String() string {
	return fmt.Sprintf("LDAPConfig{Host: %s, Port: %d, Username: %s, UserBaseDN: %s, GroupBaseDN %s, ConfigMapName: %s}", l.Host, l.Port, l.Username, l.UserBaseDN, l.GroupBaseDN, l.LibNSSLDAPConfigMapName)
}

func MergeEmpty[T any](dst, src *T) {
	dv := reflect.ValueOf(dst).Elem()
	sv := reflect.ValueOf(src).Elem()

	for i := 0; i < dv.NumField(); i++ {
		dstField := dv.Field(i)
		srcField := sv.Field(i)

		// Only update if the field is a non-empty string
		if dstField.Kind() == reflect.String && dstField.String() == "" {
			dstField.Set(srcField)
		}
	}
}

// Function to process features
func processFeatures(appConfig *AppConfig) error {
	config := appConfig.Config
	secretsDir := appConfig.SecretsDir

	for featureName, featureConfig := range config.Features {
		fmt.Printf("Processing feature: %s\n", featureName)
		switch featureName {
		case "ldap":
			ldapConfig := &LDAPConfig{}
			// Convert featureConfig (map[string]interface{}) to LDAPConfig
			configBytes, _ := json.Marshal(featureConfig)
			if err := json.Unmarshal(configBytes, ldapConfig); err != nil {
				return fmt.Errorf("failed to parse LDAP configuration: %v", err)
			}
			// Load LDAP password from secret
			ldapSecretPath := filepath.Join(secretsDir, "ldap-password", "password")
			password, err := os.ReadFile(ldapSecretPath)
			if err != nil {
				return fmt.Errorf("failed to read LDAP password from secret: %v", err)
			}
			ldapConfig.Password = string(password)
			// Store ldapConfig in appConfig for later use
			appConfig.LDAPConfig = ldapConfig
			if appConfig.LDAPConfig.Port == 0 {
				appConfig.LDAPConfig.Port = 389
			}
			libNSLDAPConfigMapName, nssConfigmapfound := config.Meta["libnss_ldap_config_map_name"]
			if nssConfigmapfound {
				appConfig.LDAPConfig.LibNSSLDAPConfigMapName = libNSLDAPConfigMapName
			}

			// Proceed with LDAP initialization if needed
			fmt.Println(ldapConfig)
		default:
			fmt.Printf("Unknown feature: %s\n", featureName)
		}
	}
	return nil
}

// InitializeAppConfig initializes the global appConfig variable
func InitializeAppConfig(configPath, mapsDir, secretsDir string) (*AppConfig, error) {
	// Load the main configuration
	config, err := loadConfig(configPath)
	if err != nil {
		return nil, fmt.Errorf("failed to load configuration: %v", err)
	}

	// Create the appConfig instance
	appConfig := &AppConfig{
		ConfigPath: configPath,
		MapsDir:    mapsDir,
		SecretsDir: secretsDir,
		Config:     config,
	}

	// Set the TLS certificate paths
	_, exists := config.Secrets["cert"]
	if !exists {
		return nil, fmt.Errorf("TLS certificate secret 'cert' not found in configuration")
	}
	tlsSecretDir := filepath.Join(secretsDir, "cert")
	appConfig.TLSCertPath = filepath.Join(tlsSecretDir, "tls.crt")
	appConfig.TLSKeyPath = filepath.Join(tlsSecretDir, "tls.key")

	// Process features and update appConfig accordingly
	if err := processFeatures(appConfig); err != nil {
		return nil, fmt.Errorf("failed to process features: %v", err)
	}

	// Initialize Kubernetes client using in-cluster config
	k8sConfig, err := rest.InClusterConfig()
	if err != nil {
		return nil, fmt.Errorf("failed to create in-cluster Kubernetes config: %v", err)
	}

	k8sClient, err := kubernetes.NewForConfig(k8sConfig)
	if err != nil {
		return nil, fmt.Errorf("failed to create Kubernetes client: %v", err)
	}

	// Set the Kubernetes client handle in the appConfig
	appConfig.K8sClient = k8sClient

	slog.Info("Kubernetes client successfully initialized")

	return appConfig, nil
}

// ReadUserProfilesFromFile reads a UserProfiles instance from a JSON file.
//
// This function constructs a file path from a directory and basename, checks for
// the file's existence, and reads its content. It then deserializes the JSON
// content into a UserProfiles instance. The function handles and returns errors
// related to file existence, reading, and JSON unmarshalling.
//
// Parameters:
// - basename: The base name of the file (without the .json extension).
// - directory: The directory where the file is located.
//
// Returns:
// - A pointer to a UserProfiles instance.
// - An error, nil if the operation is successful.
//
// Usage:
//
//	features, err := ReadUserProfilesFromFile(basename, directory)
func ReadUserProfilesFromFile(basename, directory string) (*UserProfiles, error) {
	filePath := filepath.Join(directory, basename)

	// Check if the file exists
	if _, err := os.Stat(filePath); os.IsNotExist(err) {
		return nil, nil
	}

	// Read the file
	fileData, err := os.ReadFile(filePath)
	if err != nil {
		return nil, fmt.Errorf("error reading file: %s", err)
	}

	// Deserialize the JSON content into a UserProfiles instance
	var features UserProfiles
	err = json.Unmarshal(fileData, &features)
	if err != nil {
		return nil, fmt.Errorf("error unmarshalling JSON: %s", err)
	}

	return &features, nil
}

func extractCN(dn string) string {
	parts := strings.Split(dn, ",")
	for _, part := range parts {
		if strings.HasPrefix(strings.TrimSpace(part), "cn=") {
			return strings.TrimPrefix(strings.TrimSpace(part), "cn=")
		}
	}
	return ""
}

// Example usage within your existing searchLDAP function or another appropriate part of your code:
// Assuming `groupDNs` is a slice of strings fetched from the `memberOf` attribute
func extractGroupNames(groupDNs []string) []string {
	groupCNs := make([]string, len(groupDNs))
	for i, dn := range groupDNs {
		groupCNs[i] = extractCN(dn)
	}
	return groupCNs
}

func searchLDAP(username string) (*User, error) {
	ldapConfig := appConfig.LDAPConfig
	if ldapConfig == nil {
		return nil, fmt.Errorf("LDAP configuration not initialized")
	}

	// Connect to LDAP
	l, err := ldap.Dial("tcp", fmt.Sprintf("%s:%d", ldapConfig.Host, ldapConfig.Port))
	if err != nil {
		return nil, err
	}
	defer l.Close()

	// Bind with credentials
	err = l.Bind(ldapConfig.Username, ldapConfig.Password)
	if err != nil {
		return nil, err
	}

	// Search for the given username
	searchRequest := ldap.NewSearchRequest(
		ldapConfig.UserBaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(uid=%s)", ldap.EscapeFilter(username)),
		[]string{
			"uid",
			"cn",
			"sn",
			"givenName",
			"displayName",
			"mail",
			"telephoneNumber",
			"o",
			"ou",
			"runAsUser",
			"runAsGroup",
			"fsGroup",
			"supplementalGroups",
			"memberOf",
			"userAlias",

			// posixAccount attributes
			"uidNumber",
			"gidNumber",
			"homeDirectory",
			"loginShell",
		},
		nil,
	)

	sr, err := l.Search(searchRequest)
	if err != nil {
		return nil, err
	}

	if len(sr.Entries) == 0 {
		slog.Info(fmt.Sprintf("LDAP User not found: %s", username))
		return nil, nil
	}

	entry := sr.Entries[0]
	user := &User{
		UID:                entry.GetAttributeValue("uid"),
		CommonName:         entry.GetAttributeValue("cn"),
		Surname:            entry.GetAttributeValue("sn"),
		GivenName:          entry.GetAttributeValue("givenName"),
		DisplayName:        entry.GetAttributeValue("displayName"),
		Email:              entry.GetAttributeValue("mail"),
		Telephone:          entry.GetAttributeValue("telephoneNumber"),
		Organization:       entry.GetAttributeValue("o"),
		OrganizationalUnit: entry.GetAttributeValue("ou"),
		RunAsUser:          entry.GetAttributeValue("runAsUser"),
		RunAsGroup:         entry.GetAttributeValue("runAsGroup"),
		FsGroup:            entry.GetAttributeValue("fsGroup"),
		SupplementalGroups: entry.GetAttributeValues("supplementalGroups"),
		Groups:             extractGroupNames(entry.GetAttributeValues("memberOf")),
		UserAlias:          entry.GetAttributeValue("userAlias"),

		// posixAccount attributes
		UIDNumber:     entry.GetAttributeValue("uidNumber"),
		GIDNumber:     entry.GetAttributeValue("gidNumber"),
		HomeDirectory: entry.GetAttributeValue("homeDirectory"),
		LoginShell:    entry.GetAttributeValue("loginShell"),
	}

	// Fetch posixGroups the user belongs to
	posixGroupSearchRequest := ldap.NewSearchRequest(
		ldapConfig.GroupBaseDN,
		ldap.ScopeWholeSubtree, ldap.NeverDerefAliases, 0, 0, false,
		fmt.Sprintf("(&(objectClass=posixGroup)(memberUid=%s))", ldap.EscapeFilter(user.UID)),
		[]string{"cn", "gidNumber", "memberUid"},
		nil,
	)

	posixGroupSearchResult, err := l.Search(posixGroupSearchRequest)
	if err != nil {
		return nil, err
	}

	posixGroups := make([]PosixGroup, len(posixGroupSearchResult.Entries))
	for i, entry := range posixGroupSearchResult.Entries {
		posixGroups[i] = PosixGroup{
			CN:         entry.GetAttributeValue("cn"),
			GIDNumber:  entry.GetAttributeValue("gidNumber"),
			MemberUIDs: entry.GetAttributeValues("memberUid"),
		}
	}

	user.PosixGroups = posixGroups

	if user.UserAlias != "" {
		slog.Info("found alias for user", "alias", user.UserAlias)
		if userAlias, _ := searchLDAP(user.UserAlias); userAlias != nil {
			slog.Info("merging users", "user", user.UID, "alias", user.UserAlias)
			MergeEmpty(user, userAlias)
			if uidNumber, err := strconv.Atoi(user.UIDNumber); err == nil {
				if uidNumber == -1 {
					user.UIDNumber = userAlias.UIDNumber
				}
			}
			if gidNumber, err := strconv.Atoi(user.GIDNumber); err == nil {
				if gidNumber == -1 {
					user.GIDNumber = userAlias.GIDNumber
				}
			}
		}
	}

	slog.Info("retrieved LDAP user: ", "user", user)

	return user, nil
}

// ExtractUsernameFromAdmissionReview extracts the 'username' label from a Deployment
// in an AdmissionReview.
//
// This function decodes a Deployment object from the raw object in an
// AdmissionReview request. It then looks for and extracts the 'username' label
// from the Deployment's metadata. The function returns an error if it fails to
// unmarshal the Deployment or if the 'username' label is not found.
//
// Parameters:
// - review: An AdmissionReview object containing the Deployment.
//
// Returns:
// - The extracted 'username' label as a string.
// - An error, nil if the extraction is successful.
//
// Usage:
//
//	username, err := ExtractUsernameFromAdmissionReview(admissionReview)
func ExtractUsernameFromAdmissionReview(review admissionv1.AdmissionReview) (string, error) {
	// Decode the raw object to a Deployment
	var deployment appsv1.Deployment
	if err := json.Unmarshal(review.Request.Object.Raw, &deployment); err != nil {
		return "", fmt.Errorf("error unmarshalling deployment: %v", err)
	}

	// Extract the 'username' label
	username, ok := deployment.ObjectMeta.Labels["username"]
	if !ok {
		return "", fmt.Errorf("label 'username' not found in deployment")
	}

	return username, nil
}

// GetK8sVolumeMounts converts VolumeConfig's VolumeMounts to corev1.VolumeMount slice.
//
// This function takes a VolumeConfig object and iterates through its VolumeMounts,
// converting each to a corev1.VolumeMount. It's useful for transforming custom
// volume mount configurations into Kubernetes VolumeMount objects. The function
// creates a slice of corev1.VolumeMounts, each corresponding to a mount defined
// in the VolumeConfig.
//
// Parameters:
// - config: A VolumeConfig object containing VolumeMounts for conversion.
//
// Returns:
// - A slice of corev1.VolumeMount representing Kubernetes volume mounts.
//
// Usage:
//
//	volumeMounts := GetK8sVolumeMounts(volumeConfig)
func GetK8sVolumeMounts(config VolumeConfig) []corev1.VolumeMount {
	var k8sVolumeMounts []corev1.VolumeMount

	for _, vm := range config.VolumeMounts {
		k8sVolumeMount := corev1.VolumeMount{
			Name:      vm.Name,
			MountPath: vm.MountPath,
		}
		k8sVolumeMounts = append(k8sVolumeMounts, k8sVolumeMount)
	}

	return k8sVolumeMounts
}

// parseVolumeSource interprets a string to create a corev1.VolumeSource.
//
// This function takes a string representing a volume source and converts it into
// a corev1.VolumeSource object. It supports different schemes indicated by a prefix
// followed by '://'. The default assumption is a PersistentVolumeClaim (PVC) if no
// scheme is provided. It handles 'pvc' for PVC claims and 'nfs' for NFS volumes,
// and can be extended to support more schemes.
// The function returns an error for empty claim names, invalid NFS targets, or
// unrecognized schemes.
//
// Parameters:
// - source: The string representing the volume source.
//
// Returns:
// - A corev1.VolumeSource object constructed from the input string.
// - An error, nil if the conversion is successful.
//
// Usage:
//
//	volumeSource, err := parseVolumeSource(sourceStr)
func parseVolumeSource(source string) (corev1.VolumeSource, error) {
	// Split the source string by "://"
	parts := strings.SplitN(source, "://", 2)

	// Default to assuming the source is a PVC claim name
	if len(parts) == 1 {
		if parts[0] == "" {
			return corev1.VolumeSource{}, fmt.Errorf("PVC claim name cannot be empty")
		}
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: parts[0],
			},
		}, nil
	}

	// Handle different schemes
	scheme := parts[0]
	switch scheme {
	case "pvc":
		if parts[1] == "" {
			return corev1.VolumeSource{}, fmt.Errorf("PVC claim name cannot be empty")
		}
		return corev1.VolumeSource{
			PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
				ClaimName: parts[1],
			},
		}, nil
	case "nfs":
		// Split the NFS target into server and path
		nfsTarget := strings.SplitN(parts[1], "/", 2)
		if len(nfsTarget) < 2 || nfsTarget[0] == "" || nfsTarget[1] == "" {
			return corev1.VolumeSource{}, fmt.Errorf("invalid NFS target: must include non-empty server name and path")
		}
		return corev1.VolumeSource{
			NFS: &corev1.NFSVolumeSource{
				Server: nfsTarget[0],
				Path:   "/" + nfsTarget[1],
			},
		}, nil
		// Add cases for other schemes here
	}

	// Fallback to a default type if no recognized scheme is provided
	return corev1.VolumeSource{}, fmt.Errorf("unrecognized volume source scheme: %s", scheme)
}

// GetK8sVolumes converts VolumeConfig's VolumeSources to a slice of corev1.Volume.
//
// This function iterates over VolumeSources in a VolumeConfig, converting each
// to a corev1.Volume. It uses parseVolumeSource for conversion. This function
// is essential for transforming custom volume configurations into Kubernetes
// Volume objects. If an error occurs during conversion, the function returns
// the error and stops processing.
//
// Parameters:
// - config: VolumeConfig containing VolumeSources for conversion.
//
// Returns:
// - A slice of corev1.Volume representing Kubernetes volumes.
// - An error, nil if the operation is successful.
//
// Usage:
//
//	volumes, err := GetK8sVolumes(volumeConfig)
func GetK8sVolumes(config VolumeConfig) ([]corev1.Volume, error) {
	var k8sVolumes []corev1.Volume

	for _, vs := range config.VolumeSources {
		if volumeSource, err := parseVolumeSource(vs.Source); err == nil {
			k8sVolume := corev1.Volume{
				Name:         vs.Name,
				VolumeSource: volumeSource,
			}
			k8sVolumes = append(k8sVolumes, k8sVolume)
		} else {
			return nil, err
		}
	}

	return k8sVolumes, nil
}

func GetK8sEnvFrom(secretsFrom []SecretRef) []corev1.EnvFromSource {
	var envFromSources []corev1.EnvFromSource

	for _, secretRef := range secretsFrom {
		envFromSource := corev1.EnvFromSource{
			SecretRef: &corev1.SecretEnvSource{
				LocalObjectReference: corev1.LocalObjectReference{
					Name: secretRef.SecretName,
				},
			},
		}
		envFromSources = append(envFromSources, envFromSource)
	}

	return envFromSources
}

func mergeSortedInt64Slices(a, b []int64) []int64 {
	var result []int64
	i, j := 0, 0
	for i < len(a) && j < len(b) {
		if a[i] == b[j] {
			result = append(result, a[i])
			i++
			j++
		} else if a[i] < b[j] {
			result = append(result, a[i])
			i++
		} else {
			result = append(result, b[j])
			j++
		}
	}
	// Append remaining elements from a
	for i < len(a) {
		result = append(result, a[i])
		i++
	}
	// Append remaining elements from b
	for j < len(b) {
		result = append(result, b[j])
		j++
	}
	return result
}

func constructSupplementalGroups(user *User) []int64 {
	var supplementalGroups []int64
	// Parse GID numbers from user.SupplementalGroups
	for _, sg := range user.SupplementalGroups {
		sgInt, err := strconv.ParseInt(sg, 10, 64)
		if err != nil {
			slog.Error("Invalid SupplementalGroup", "group", sg, "err", err)
			continue
		}
		supplementalGroups = append(supplementalGroups, sgInt)
	}

	var posixGroupGIDs []int64
	// Parse GID numbers from user.PosixGroups
	for _, pg := range user.PosixGroups {
		gidInt, err := strconv.ParseInt(pg.GIDNumber, 10, 64)
		if err != nil {
			slog.Error("Invalid GIDNumber found in PosixGroup", "gid", pg.GIDNumber, "group", pg.CN, "err", err)
			continue
		}
		posixGroupGIDs = append(posixGroupGIDs, gidInt)
	}

	// Sort and remove duplicates from each list
	sort.Slice(supplementalGroups, func(i, j int) bool { return supplementalGroups[i] < supplementalGroups[j] })

	sort.Slice(posixGroupGIDs, func(i, j int) bool { return posixGroupGIDs[i] < posixGroupGIDs[j] })

	// Merge the two sorted lists, skipping duplicates
	mergedGroups := mergeSortedInt64Slices(supplementalGroups, posixGroupGIDs)

	return mergedGroups
}

func constructSecurityContexts(user *User) (*corev1.PodSecurityContext, *corev1.SecurityContext, error) {
	var podSecurityContext corev1.PodSecurityContext
	var securityContext corev1.SecurityContext

	// Parse RunAsUser for SecurityContext
	if user.RunAsUser != "" {
		runAsUser, err := strconv.ParseInt(user.RunAsUser, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid RunAsUser: %v", err)
		}
		securityContext.RunAsUser = &runAsUser
		podSecurityContext.RunAsUser = &runAsUser
	} else if user.UIDNumber != "" {
		runAsUser, err := strconv.ParseInt(user.UIDNumber, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid RunAsUser: %v", err)
		}
		securityContext.RunAsUser = &runAsUser
		podSecurityContext.RunAsUser = &runAsUser
	}

	// Parse RunAsGroup for SecurityContext
	if user.RunAsGroup != "" {
		runAsGroup, err := strconv.ParseInt(user.RunAsGroup, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid RunAsGroup: %v", err)
		}
		securityContext.RunAsGroup = &runAsGroup
		podSecurityContext.RunAsGroup = &runAsGroup
	} else if user.GIDNumber != "" {
		runAsGroup, err := strconv.ParseInt(user.GIDNumber, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid RunAsGroup: %v", err)
		}
		securityContext.RunAsGroup = &runAsGroup
		podSecurityContext.RunAsGroup = &runAsGroup
	}

	// Parse FsGroup for PodSecurityContext
	if user.FsGroup != "" {
		fsGroup, err := strconv.ParseInt(user.FsGroup, 10, 64)
		if err != nil {
			return nil, nil, fmt.Errorf("invalid FsGroup: %v", err)
		}
		podSecurityContext.FSGroup = &fsGroup
	}

	podSecurityContext.SupplementalGroups = constructSupplementalGroups(user)

	// Set the FOWNER capability in the security context
	if securityContext.Capabilities == nil {
		securityContext.Capabilities = &corev1.Capabilities{}
	}
	//securityContext.Capabilities.Add = append(securityContext.Capabilities.Add, corev1.Capability("CHOWN"))

	return &podSecurityContext, &securityContext, nil
}

func constructEnv(user *User) []corev1.EnvVar {
	env := make([]corev1.EnvVar, 0)

	env = append(env, corev1.EnvVar{
		Name:  "USER_IDENTITY",
		Value: "ldap",
	})

	if user.HomeDirectory != "" {
		env = append(env, corev1.EnvVar{
			Name:  "HOME",
			Value: user.HomeDirectory,
		})
	}
	return env
}

func getPVCsByLabel(clientset *kubernetes.Clientset, groupName, namespace string) ([]corev1.PersistentVolumeClaim, error) {
	// Define the label selector to filter by the "helx.renci.org/group-name" label
	labelSelector := fmt.Sprintf("helx.renci.org/group-name=%s", groupName)

	// Get the list of PVCs in the specified namespace with the label selector
	pvcList, err := clientset.CoreV1().PersistentVolumeClaims(namespace).List(context.Background(), metav1.ListOptions{
		LabelSelector: labelSelector,
	})
	if err != nil {
		return nil, err
	}

	if len(pvcList.Items) == 0 {
		slog.Info("no PVCs found with label helx.renci.org/group-name", "group", groupName)
	} else if len(pvcList.Items) > 1 {
		slog.Error("multiple PVCs found with label helx.renci.org/group-name", "group", groupName)
	}

	return pvcList.Items, nil
}

// getVolumesAndMountsForUserGroups constructs volumes and volume mounts for the user's groups.
func getVolumesAndMountsForUserGroups(clientset *kubernetes.Clientset, user *User, namespace string) ([]corev1.Volume, []corev1.VolumeMount, error) {
	var volumes []corev1.Volume
	var volumeMounts []corev1.VolumeMount

	for _, groupName := range user.Groups {
		// Get PVCs labeled with the group name
		pvcs, err := getPVCsByLabel(clientset, groupName, namespace)
		if err != nil {
			return nil, nil, err
		}

		// If there are PVCs for this group, take the first one
		if len(pvcs) > 0 {
			pvc := pvcs[0]

			// Use the PVC name as the volume name
			volumeName := pvc.Name

			// Create the volume
			volume := corev1.Volume{
				Name: volumeName,
				VolumeSource: corev1.VolumeSource{
					PersistentVolumeClaim: &corev1.PersistentVolumeClaimVolumeSource{
						ClaimName: pvc.Name,
					},
				},
			}

			// Create the volume mount
			volumeMount := corev1.VolumeMount{
				Name:      volumeName,
				MountPath: fmt.Sprintf("/shared/%s", groupName),
			}

			// Add to the slices
			volumes = append(volumes, volume)
			volumeMounts = append(volumeMounts, volumeMount)
		}
		// If no PVCs are found, skip this group
	}

	return volumes, volumeMounts, nil
}

func getLDAPConfigVolumesAndMounts(configMapName string) ([]corev1.Volume, []corev1.VolumeMount) {
	volumes := []corev1.Volume{
		{
			Name: "ldap-config",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{
						Name: configMapName,
					},
				},
			},
		},
	}

	volumeMounts := []corev1.VolumeMount{
		{
			Name:      "ldap-config",
			MountPath: "/etc/libnss-ldap.conf",
			SubPath:   "libnss-ldap.conf",
		},
		{
			Name:      "ldap-config",
			MountPath: "/etc/ldap.conf",
			SubPath:   "libnss-ldap.conf",
		},
		{
			Name:      "ldap-config",
			MountPath: "/etc/nsswitch.conf",
			SubPath:   "nsswitch.conf",
		},
	}

	return volumes, volumeMounts
}

// printVolumes logs the details of each Volume in the provided slice.
//
// This function iterates over a slice of corev1.Volume and logs their details,
// including name and volume source. It is primarily used for debugging and
// logging purposes, offering a quick overview of the volumes configured in a
// Kubernetes environment.
//
// Parameters:
// - volumes: A slice of corev1.Volume to be logged.
//
// Usage:
//
//	printVolumes(volumes)
func printVolumes(volumes []corev1.Volume) {
	slog.Debug("Volumes:")
	for _, volume := range volumes {
		slog.Debug("name", volume.Name, slog.Any("source", volume.VolumeSource))
	}
}

// printVolumeMounts logs details of each VolumeMount in the given slice.
//
// This function goes through a slice of corev1.VolumeMount and logs their
// details, such as name, mount path, and read-only status. It's mainly used
// for debugging and logging, offering a clear overview of volume mounts in
// Kubernetes environments.
//
// Parameters:
// - volumeMounts: Slice of corev1.VolumeMount to be logged.
//
// Usage:
//
//	printVolumeMounts(volumeMounts)
func printVolumeMounts(volumeMounts []corev1.VolumeMount) {
	slog.Debug("VolumeMounts:")
	for _, mount := range volumeMounts {
		slog.Debug("Mount Info", "name", mount.Name, "path", mount.MountPath, "read only", mount.ReadOnly)
	}
}

/*
// prettyPrintJSON formats a JSON string with indentation for readability.
//
// This function takes a JSON string and uses json.Indent to add indentation
// (4 spaces). It's useful for enhancing the readability of JSON data,
// particularly for logging or debugging purposes. On formatting errors,
// it returns an empty string and the error.
//
// Parameters:
// - inputJSON: The JSON data string to format.
//
// Returns:
// - A formatted JSON string with indentation.
// - An error object, nil if the operation is successful.
//
// Usage:
//
//	formattedJSON, err := prettyPrintJSON(rawJSON)
func prettyPrintJSON(inputJSON string) (string, error) {
	var buffer bytes.Buffer
	err := json.Indent(&buffer, []byte(inputJSON), "", "    ")
	if err != nil {
		return "", err
	}
	return buffer.String(), nil
}
*/

// printPatchOperations prints each JsonPatchOperation in the provided slice.
//
// This function iterates over a slice of jsonpatch.JsonPatchOperation and
// prints each operation in a formatted JSON structure. It handles errors in
// marshalling the JsonPatchOperation and logs them, continuing to the next
// operation if any error occurs.
//
// The function is primarily used for debugging purposes, providing a clear
// visual representation of each patch operation created during the admission
// control process.
//
// Parameters:
// - operations: A slice of jsonpatch.JsonPatchOperation to be printed.
//
// Usage:
//
//	printPatchOperations(patchOperations)
func printPatchOperations(operations []jsonpatch.JsonPatchOperation) {
	slog.Debug("Patches:")
	for i, op := range operations {
		opJSON, err := json.MarshalIndent(op, "", "    ")
		if err != nil {
			slog.Error("Failed to marshal operation", "num", i, "err", err)
			continue
		}
		slog.Debug("op", "num", i, "patch", opJSON)
	}
}

// applyResourcesToContainers applies the given resources to each container in the slice.
func applyResourcesToContainers(containers []corev1.Container, resources ProfileResources) {
	for i := range containers {
		container := &containers[i]

		// Add volume mounts
		container.VolumeMounts = append(container.VolumeMounts, resources.VolumeMounts...)

		// Apply SecurityContext
		if resources.SecurityContext != nil {
			container.SecurityContext = resources.SecurityContext
		}

		// Add envFrom sources
		container.EnvFrom = append(container.EnvFrom, resources.EnvFromSources...)
		container.Env = append(container.Env, resources.Env...)
	}
}

func calculatePatch(admissionReview *admissionv1.AdmissionReview, resources ProfileResources) ([]byte, error) {
	// Deserialize the original Deployment from the AdmissionReview
	var originalDeployment appsv1.Deployment
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &originalDeployment); err != nil {
		return nil, err
	}

	// Apply modifications to the Deployment by starting with a copy
	modifiedDeployment := originalDeployment.DeepCopy()

	// Add volumes
	modifiedDeployment.Spec.Template.Spec.Volumes = append(modifiedDeployment.Spec.Template.Spec.Volumes, resources.Volumes...)

	// Apply modifications to the Containers
	applyResourcesToContainers(modifiedDeployment.Spec.Template.Spec.Containers, resources)

	// Apply modifications to the InitContainers
	applyResourcesToContainers(modifiedDeployment.Spec.Template.Spec.InitContainers, resources)

	// Apply PodSecurityContext
	if resources.PodSecurityContext != nil {
		modifiedDeployment.Spec.Template.Spec.SecurityContext = resources.PodSecurityContext
	}

	slog.Info("marshalling original new JSON")
	modifiedJSON, err := json.Marshal(modifiedDeployment)
	if err != nil {
		return nil, err
	}

	// Create patch
	patchOps, err := jsonpatch.CreatePatch(admissionReview.Request.Object.Raw, modifiedJSON)
	if err != nil {
		return nil, err
	}

	printPatchOperations(patchOps)

	// Marshal patch to JSON
	patchBytes, err := json.Marshal(patchOps)
	if err != nil {
		return nil, err
	}

	return patchBytes, nil
}

func appendProfiles(featureKey string, resources ProfileResources) (ProfileResources, error) {
	profilePath := filepath.Join(appConfig.MapsDir, "user_profiles")

	userProfiles, err := ReadUserProfilesFromFile(featureKey, profilePath)
	if err != nil {
		return resources, fmt.Errorf("user feature spec for %s invalid: %v", featureKey, err)
	}
	if userProfiles != nil {
		specificVolumes, err := GetK8sVolumes(userProfiles.Volumes)
		if err != nil {
			return resources, fmt.Errorf("volume spec for %s invalid: %v", featureKey, err)
		}
		resources.Volumes = append(resources.Volumes, specificVolumes...)
		resources.VolumeMounts = append(resources.VolumeMounts, GetK8sVolumeMounts(userProfiles.Volumes)...)
		resources.EnvFromSources = append(resources.EnvFromSources, GetK8sEnvFrom(userProfiles.SecretsFrom)...)
	}
	return resources, nil
}

func setSecurityContexts(user *User, resources ProfileResources) (ProfileResources, error) {
	podSecurityContext, securityContext, err := constructSecurityContexts(user)
	if err != nil {
		return resources, fmt.Errorf("failed to construct security contexts: %v", err)
	}
	resources.PodSecurityContext = podSecurityContext
	resources.SecurityContext = securityContext
	return resources, nil
}

func setEnvVars(user *User, resources ProfileResources) ProfileResources {
	resources.Env = constructEnv(user)
	return resources
}

func addGroupsToProfile(clientset *kubernetes.Clientset, user *User, namespace string, resources ProfileResources) (ProfileResources, error) {
	volumes, volumeMounts, err := getVolumesAndMountsForUserGroups(clientset, user, namespace)
	if err != nil {
		return resources, fmt.Errorf("could not detect group PVCs for user %s: %v", user.UID, err)
	}
	resources.Volumes = append(resources.Volumes, volumes...)
	resources.VolumeMounts = append(resources.VolumeMounts, volumeMounts...)
	return resources, nil
}

func addLibNSSLDAPConfigToProfile(configMapName string, resources ProfileResources) ProfileResources {
	volumes, volumeMounts := getLDAPConfigVolumesAndMounts(configMapName)
	resources.Volumes = append(resources.Volumes, volumes...)
	resources.VolumeMounts = append(resources.VolumeMounts, volumeMounts...)
	return resources
}

func processAdmissionReview(admissionReview admissionv1.AdmissionReview) *admissionv1.AdmissionResponse {
	// Implement your logic here
	// For example, always allow the request:
	slog.Info("processing admission", "namespace", admissionReview.Request.Namespace, "deployment", admissionReview.Request.Name)

	// Deserialize the AdmissionReview to a Deployment object
	var deployment appsv1.Deployment
	if err := json.Unmarshal(admissionReview.Request.Object.Raw, &deployment); err != nil {
		slog.Error("failed to unmarshall deployment", "err", err)
		return &admissionv1.AdmissionResponse{Allowed: true}
	}

	if username, err := ExtractUsernameFromAdmissionReview(admissionReview); err == nil {
		slog.Info("altering user deployment", "user", username)

		var err error
		resources := ProfileResources{Volumes: []corev1.Volume{}, VolumeMounts: []corev1.VolumeMount{}, EnvFromSources: []corev1.EnvFromSource{}}

		if resources, err = appendProfiles("auto", resources); err != nil {
			slog.Error("failed to add auto features", "user", username, "err", err)
		}

		if resources, err = appendProfiles(username+".json", resources); err != nil {
			slog.Error("failed to add user features", "user", username, "err", err)
		}

		// Search LDAP for user information
		user, err := searchLDAP(username)
		if err != nil {
			slog.Error("failed to retrieve user from LDAP", "user", username, "err", err)
		} else if user != nil {
			if resources, err = setSecurityContexts(user, resources); err != nil {
				slog.Error("failed to construct security contexts", "user", username, "err", err)
			}
			if resources, err = addGroupsToProfile(appConfig.K8sClient, user, admissionReview.Request.Namespace, resources); err != nil {
				slog.Error("failed to add group volumes", "err", err)
			}
			if appConfig.LDAPConfig.LibNSSLDAPConfigMapName != "" {
				resources = addLibNSSLDAPConfigToProfile(appConfig.LDAPConfig.LibNSSLDAPConfigMapName, resources)
			}
			resources = setEnvVars(user, resources)
		}

		printVolumes(resources.Volumes)
		printVolumeMounts(resources.VolumeMounts)

		// Calculate the patch
		if patchBytes, err := calculatePatch(&admissionReview, resources); err != nil {
			slog.Error("patch creation failed", "err", err)
		} else {
			return &admissionv1.AdmissionResponse{
				UID:     admissionReview.Request.UID,
				Allowed: true,
				Patch:   patchBytes,
				PatchType: func() *admissionv1.PatchType {
					pt := admissionv1.PatchTypeJSONPatch
					return &pt
				}(),
			}
		}
	} else {
		slog.Error("username not detected", "err", err)
	}
	return &admissionv1.AdmissionResponse{
		UID:     admissionReview.Request.UID,
		Allowed: true,
	}
}

// handleAdmissionReview processes an HTTP request for Kubernetes admission control.
//
// This function reads and decodes an AdmissionReview request from the HTTP
// request body, performs custom logic (handled in processAdmissionReview),
// and then sends back an AdmissionReview response. It manages errors like
// reading the request body, unmarshalling JSON data, and marshalling the
// response, responding with appropriate HTTP error codes and messages.
//
// The function expects an HTTP request with a JSON body representing an
// AdmissionReview object. It sends back a JSON-encoded AdmissionReview response.
//
// Usage:
//
//	http.HandleFunc("/admission-review", handleAdmissionReview)
func handleAdmissionReview(w http.ResponseWriter, r *http.Request) {
	// Read the body of the request
	body, err := io.ReadAll(r.Body)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not read request body: %v", err), http.StatusBadRequest)
		return
	}

	// Decode the AdmissionReview request
	var admissionReviewReq admissionv1.AdmissionReview
	if err := json.Unmarshal(body, &admissionReviewReq); err != nil {
		http.Error(w, fmt.Sprintf("could not unmarshal request: %v", err), http.StatusBadRequest)
		return
	}

	// Process the request and prepare the response
	// This is where your custom logic will go
	admissionResponse := processAdmissionReview(admissionReviewReq)

	// Encode the response
	admissionReviewResp := admissionv1.AdmissionReview{
		TypeMeta: admissionReviewReq.TypeMeta, // Use the same TypeMeta as the request
		Response: admissionResponse,
	}
	resp, err := json.Marshal(admissionReviewResp)
	if err != nil {
		http.Error(w, fmt.Sprintf("could not marshal response: %v", err), http.StatusInternalServerError)
		return
	}

	// Write the response
	w.Header().Set("Content-Type", "application/json")
	w.WriteHeader(http.StatusOK)
	w.Write(resp)
}

// readinessHandler checks the readiness of the service to handle requests.
// In this implementation, it always indicates that the service is ready by
// returning a 200 OK status. In more complex scenarios, this function could
// check internal conditions before determining readiness.
func readinessHandler(w http.ResponseWriter, r *http.Request) {
	// Check conditions to determine if service is ready to handle requests.
	// For simplicity, we're always returning 200 OK in this example.
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Ready"))
}

// livenessHandler checks the health of the service to ensure it's running and
// operational. In this implementation, it always indicates that the service is
// alive by returning a 200 OK status. In more advanced scenarios, this function
// could check internal health metrics before determining liveness.
func livenessHandler(w http.ResponseWriter, r *http.Request) {
	// Check conditions to determine if service is alive and healthy.
	// For simplicity, we're always returning 200 OK in this example.
	w.WriteHeader(http.StatusOK)
	w.Write([]byte("Alive"))
}

func main() {
	var err error

	// Create a logger with a TextHandler at the Info level:
	logger := slog.New(slog.NewTextHandler(os.Stdout, &slog.HandlerOptions{
		Level: slog.LevelInfo,
	}))
	slog.SetDefault(logger)

	// Paths
	configPath := "/etc/user-mutator-config/config.json"
	mapsDir := "/etc/user-mutator-maps"
	secretsDir := "/etc/user-mutator-secrets"

	// Initialize the global appConfig
	if appConfig, err = InitializeAppConfig(configPath, mapsDir, secretsDir); err != nil {
		slog.Error("Initialization error", "err", err)
		os.Exit(1)
	}

	r := mux.NewRouter()
	r.HandleFunc("/mutate", handleAdmissionReview)
	r.HandleFunc("/readyz", readinessHandler)
	r.HandleFunc("/healthz", livenessHandler)
	http.Handle("/", r)
	slog.Info("Server started on :8443")

	if err := http.ListenAndServeTLS(":8443", appConfig.TLSCertPath, appConfig.TLSKeyPath, nil); err != nil {
		slog.Error("Failed to start server", "err", err)
		os.Exit(1)
	}
}
