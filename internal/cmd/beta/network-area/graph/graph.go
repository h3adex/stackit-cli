package graph

import (
	"context"
	_ "embed"
	"encoding/json"
	"errors"
	"fmt"
	"html/template"
	"log"
	"net"
	"net/http"
	"os"
	"os/exec"
	"os/signal"
	"runtime"
	"strings"
	"syscall"
	"time"

	"github.com/spf13/cobra"
	"github.com/stackitcloud/stackit-cli/internal/pkg/args"
	"github.com/stackitcloud/stackit-cli/internal/pkg/examples"
	"github.com/stackitcloud/stackit-cli/internal/pkg/flags"
	"github.com/stackitcloud/stackit-cli/internal/pkg/globalflags"
	"github.com/stackitcloud/stackit-cli/internal/pkg/print"
	iaasClient "github.com/stackitcloud/stackit-cli/internal/pkg/services/iaas/client"
	resourceManagerClient "github.com/stackitcloud/stackit-cli/internal/pkg/services/resourcemanager/client"
	"github.com/stackitcloud/stackit-cli/internal/pkg/types"
	"github.com/stackitcloud/stackit-cli/internal/pkg/utils"

	"github.com/stackitcloud/stackit-sdk-go/services/iaas"
	"github.com/stackitcloud/stackit-sdk-go/services/resourcemanager"
)

//go:embed html/index.html
var indexHTML string

const (
	areaIdArg          = "AREA_ID"
	organizationIdFlag = "organization-id"
	apiDelayFlag       = "api-delay"
	maxRetries         = 3
)

type inputModel struct {
	*globalflags.GlobalFlagModel
	OrganizationId *string
	AreaId         string
	ApiDelay       time.Duration
}

type Node struct {
	ID    string `json:"id"`
	Label string `json:"label"`
	Group string `json:"group,omitempty"`
}

type Edge struct {
	From  string `json:"from"`
	To    string `json:"to"`
	Class string `json:"class,omitempty"` // Used for styling (e.g., "peering")
}

type HtmlGraphData struct {
	Nodes []Node `json:"nodes"`
	Edges []Edge `json:"edges"`
}

func NewCmd(params *types.CmdParams) *cobra.Command {
	cmd := &cobra.Command{
		Use:   fmt.Sprintf("graph %s", areaIdArg),
		Short: "Opens an interactive browser graph of a STACKIT Network Area",
		// TODO: explain further that this is a experiment and community driven
		Long: "Spins up a local web server to display an interactive, force-directed graph of a Network Area, its Projects, Networks, Interfaces, and Routing Tables.",
		Args: args.SingleArg(areaIdArg, utils.ValidateUUID),
		Example: examples.Build(
			examples.NewExample(
				`Visualize network area with ID "xxx" in organization with ID "yyy"`,
				"$ stackit network-area graph xxx --organization-id yyy",
			),
			examples.NewExample(
				`Visualize network area with a custom 500ms delay between API calls`,
				"$ stackit network-area graph xxx --organization-id yyy --api-delay 500",
			),
		),
		RunE: func(cmd *cobra.Command, args []string) error {
			ctx := context.Background()
			model, err := parseInput(params.Printer, cmd, args)
			if err != nil {
				return err
			}

			iaasApiClient, err := iaasClient.ConfigureClient(params.Printer, params.CliVersion)
			if err != nil {
				return err
			}

			resourceManagerApiClient, err := resourceManagerClient.ConfigureClient(params.Printer, params.CliVersion)
			if err != nil {
				return err
			}

			// Discover attached projects
			params.Printer.Info(fmt.Sprintf("Discovering attached projects in the organization %s ...\n", *model.OrganizationId))
			attachedProjects, err := getAttachedProjectsInContainer(ctx, params.Printer, resourceManagerApiClient, *model.OrganizationId, model.AreaId, model.ApiDelay)
			if err != nil {
				return fmt.Errorf("error discovering projects: %w", err)
			}
			params.Printer.Info(fmt.Sprintf("Found %d attached projects.\n", len(attachedProjects)))

			// Build graph
			params.Printer.Info("Fetching network topology ...\n")
			graphData := buildGraphData(ctx, params.Printer, model, iaasApiClient, attachedProjects)

			// Serve graph
			return serveGraph(params.Printer, graphData)
		},
	}
	configureFlags(cmd)
	return cmd
}

// TODO: improve flags
func configureFlags(cmd *cobra.Command) {
	cmd.Flags().Var(flags.UUIDFlag(), organizationIdFlag, "Organization ID")
	cmd.Flags().Int64(apiDelayFlag, 200, "Delay between API calls in milliseconds to avoid rate limits")

	err := flags.MarkFlagsRequired(cmd, organizationIdFlag)
	cobra.CheckErr(err)
}

func parseInput(p *print.Printer, cmd *cobra.Command, inputArgs []string) (*inputModel, error) {
	areaId := inputArgs[0]
	globalFlags := globalflags.Parse(p, cmd)

	apiDelayMs, err := cmd.Flags().GetInt64(apiDelayFlag)
	if err != nil {
		apiDelayMs = 200
	}

	model := inputModel{
		GlobalFlagModel: globalFlags,
		OrganizationId:  flags.FlagToStringPointer(p, cmd, organizationIdFlag),
		AreaId:          areaId,
		ApiDelay:        time.Duration(apiDelayMs) * time.Millisecond,
	}

	p.DebugInputModel(model)
	return &model, nil
}

// TODO: better approach?
// Generic retry wrapper
func retryRequest[T any](p *print.Printer, delay time.Duration, action func() (*T, error)) (*T, error) {
	var lastErr error
	for attempt := 1; attempt <= maxRetries; attempt++ {
		resp, err := action()
		if err == nil {
			return resp, nil
		}
		lastErr = err

		if attempt < maxRetries {
			p.Warn(fmt.Sprintf("  API Request failed. Retrying... (Attempt %d of %d) - %v\n", attempt, maxRetries-1, err))
			time.Sleep(delay)
		}
	}
	return nil, lastErr
}

// Build Cytoscape graph data
func buildGraphData(ctx context.Context, p *print.Printer, model *inputModel, iaasApiClient *iaas.APIClient, projects []resourcemanager.Project) HtmlGraphData {
	graphData := HtmlGraphData{}
	var peeredNetworks []string // Store peered networks

	shortAreaId := model.AreaId
	if len(model.AreaId) > 8 {
		shortAreaId = model.AreaId[:8] + "..."
	}
	graphData.Nodes = append(graphData.Nodes, Node{
		ID:    model.AreaId,
		Label: "Network Area\n" + shortAreaId,
		Group: "network-area",
	})

	for _, proj := range projects {
		if proj.ProjectId == nil {
			continue
		}
		projId := *proj.ProjectId
		projName := "Unknown Project"
		if proj.Name != nil {
			projName = *proj.Name
		}

		graphData.Nodes = append(graphData.Nodes, Node{
			ID:    projId,
			Label: fmt.Sprintf("Project\n%s", projName),
			Group: "project",
		})
		graphData.Edges = append(graphData.Edges, Edge{From: model.AreaId, To: projId})

		networksResp, err := retryRequest(p, model.ApiDelay, func() (*iaas.NetworkListResponse, error) {
			time.Sleep(model.ApiDelay)
			return iaasApiClient.ListNetworks(ctx, projId, model.Region).Execute()
		})

		if err != nil {
			p.Warn(fmt.Sprintf("Warning: could not fetch networks for project %s after %d attempts: %v\n", projId, maxRetries, err))
			continue
		}

		if networksResp == nil || networksResp.Items == nil {
			continue
		}

		for _, net := range *networksResp.Items {
			if net.Id == nil {
				continue
			}
			netId := *net.Id
			netName := "Unknown Net"
			if net.Name != nil {
				netName = *net.Name
			}

			ipLabel := "No CIDR"
			if net.Ipv4 != nil && net.Ipv4.Prefixes != nil && len(*net.Ipv4.Prefixes) > 0 {
				ipLabel = strings.Join(*net.Ipv4.Prefixes, ", ")
			} else if net.Ipv6 != nil && net.Ipv6.Prefixes != nil && len(*net.Ipv6.Prefixes) > 0 {
				ipLabel = strings.Join(*net.Ipv6.Prefixes, ", ")
			}

			graphData.Nodes = append(graphData.Nodes, Node{
				ID:    netId,
				Label: fmt.Sprintf("Network\n%s\n[%s]", netName, ipLabel),
				Group: "network",
			})
			graphData.Edges = append(graphData.Edges, Edge{From: projId, To: netId})

			// Check routing table for system routes
			if net.Routed != nil && *net.Routed && net.RoutingTableId != nil {
				rtId := *net.RoutingTableId

				rtResp, err := retryRequest(p, model.ApiDelay, func() (*iaas.RoutingTable, error) {
					time.Sleep(model.ApiDelay)
					return iaasApiClient.GetRoutingTableOfAreaExecute(ctx, *model.OrganizationId, model.AreaId, model.Region, rtId)
				})

				if err == nil && rtResp != nil {
					if rtResp.SystemRoutes != nil && *rtResp.SystemRoutes == true {
						peeredNetworks = append(peeredNetworks, netId)
					}
				} else {
					p.Warn(fmt.Sprintf("Warning: could not fetch Routing Table %s after %d attempts: %v\n", rtId, maxRetries, err))
				}

				if !nodeExists(graphData.Nodes, rtId) {
					graphData.Nodes = append(graphData.Nodes, Node{
						ID:    rtId,
						Label: "Routing Table\n" + rtId[:8] + "...",
						Group: "routing-table",
					})
				}
				graphData.Edges = append(graphData.Edges, Edge{From: netId, To: rtId})
			}

			// Fetch NICs
			nicsResp, err := retryRequest(p, model.ApiDelay, func() (*iaas.NICListResponse, error) {
				time.Sleep(model.ApiDelay)
				return iaasApiClient.ListNics(ctx, projId, model.Region, netId).Execute()
			})

			if err != nil {
				p.Warn(fmt.Sprintf("Warning: could not fetch NICs for network %s after %d attempts: %v\n", netId, maxRetries, err))
				continue
			}

			if nicsResp == nil || nicsResp.Items == nil {
				continue
			}

			for _, nic := range *nicsResp.Items {
				if nic.Id == nil {
					continue
				}
				nicId := *nic.Id

				ipLabelNic := "No IP"
				if nic.Ipv4 != nil {
					ipLabelNic = *nic.Ipv4
				} else if nic.Ipv6 != nil {
					ipLabelNic = *nic.Ipv6
				}

				nicGroup := "nic-secure"
				securityStatus := "Security: ON"

				if nic.NicSecurity == nil || *nic.NicSecurity == false {
					nicGroup = "nic-insecure"
					securityStatus = "Security: OFF"
				}

				graphData.Nodes = append(graphData.Nodes, Node{
					ID:    nicId,
					Label: fmt.Sprintf("NIC\n%s\n[%s]", ipLabelNic, securityStatus),
					Group: nicGroup,
				})
				graphData.Edges = append(graphData.Edges, Edge{From: netId, To: nicId})
			}
		}
	}

	// Create full-mesh peering edges
	for i := 0; i < len(peeredNetworks); i++ {
		for j := i + 1; j < len(peeredNetworks); j++ {
			graphData.Edges = append(graphData.Edges, Edge{
				From:  peeredNetworks[i],
				To:    peeredNetworks[j],
				Class: "peering",
			})
		}
	}

	return graphData
}

// Check if node exists
func nodeExists(nodes []Node, id string) bool {
	for _, n := range nodes {
		if n.ID == id {
			return true
		}
	}
	return false
}

// Serve HTTP and open browser
func serveGraph(p *print.Printer, data HtmlGraphData) error {
	t, err := template.New("graph").Parse(indexHTML)
	if err != nil {
		return fmt.Errorf("failed to parse embedded html: %w", err)
	}

	mux := http.NewServeMux()
	mux.HandleFunc("/", func(w http.ResponseWriter, r *http.Request) {
		jsonData, err := json.Marshal(data)
		if err != nil {
			http.Error(w, err.Error(), http.StatusInternalServerError)
			return
		}
		_ = t.Execute(w, string(jsonData))
	})

	listener, err := net.Listen("tcp", "127.0.0.1:0")
	if err != nil {
		return fmt.Errorf("failed to bind to a port: %w", err)
	}

	url := fmt.Sprintf("http://%s", listener.Addr().String())
	p.Info(fmt.Sprintf("\nGraph server is running at: %s\n", url))
	p.Info("Press Ctrl+C to stop the server and return to the CLI.\n")

	go openBrowser(url)

	stop := make(chan os.Signal, 1)
	signal.Notify(stop, os.Interrupt, syscall.SIGTERM)

	server := &http.Server{Handler: mux}

	go func() {
		if err := server.Serve(listener); err != nil && !errors.Is(err, http.ErrServerClosed) {
			log.Fatalf("HTTP server error: %v", err)
		}
	}()

	<-stop
	p.Info("\nShutting down graph server...\n")
	return server.Shutdown(context.Background())
}

// Recursively find projects
func getAttachedProjectsInContainer(ctx context.Context, p *print.Printer, client *resourcemanager.APIClient, containerId string, networkAreaId string, apiDelay time.Duration) ([]resourcemanager.Project, error) {
	var attachedProjects []resourcemanager.Project

	projectsResp, err := retryRequest(p, apiDelay, func() (*resourcemanager.ListProjectsResponse, error) {
		time.Sleep(apiDelay)
		return client.ListProjects(ctx).ContainerParentId(containerId).Execute()
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list projects for container %s after retries: %w", containerId, err)
	}

	if projectsResp != nil && projectsResp.Items != nil {
		for _, proj := range *projectsResp.Items {
			if proj.Labels != nil {
				labels := *proj.Labels
				if val, exists := labels["networkArea"]; exists && val == networkAreaId {
					attachedProjects = append(attachedProjects, proj)
				}
			}
		}
	}

	foldersResp, err := retryRequest(p, apiDelay, func() (*resourcemanager.ListFoldersResponse, error) {
		time.Sleep(apiDelay)
		return client.ListFolders(ctx).ContainerParentId(containerId).Execute()
	})

	if err != nil {
		return nil, fmt.Errorf("failed to list folders for container %s after retries: %w", containerId, err)
	}

	if foldersResp != nil && foldersResp.Items != nil {
		for _, f := range *foldersResp.Items {
			if f.ContainerId != nil {
				nestedProjects, err := getAttachedProjectsInContainer(ctx, p, client, *f.ContainerId, networkAreaId, apiDelay)
				if err != nil {
					return nil, err
				}
				attachedProjects = append(attachedProjects, nestedProjects...)
			}
		}
	}

	return attachedProjects, nil
}

// TODO: reuse auth method
// Open default browser
func openBrowser(url string) {
	var err error
	switch runtime.GOOS {
	case "linux":
		err = exec.Command("xdg-open", url).Start()
	case "windows":
		err = exec.Command("rundll32", "url.dll,FileProtocolHandler", url).Start()
	case "darwin":
		err = exec.Command("open", url).Start()
	}
	if err != nil {
		fmt.Printf("Could not open browser automatically. Please click the link manually.\n")
	}
}
