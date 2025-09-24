package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"regexp"
	"sort"
	"strconv"
	"strings"
	"sync"
	"time"

	"github.com/Azure/azure-sdk-for-go/sdk/azcore/policy"
	"github.com/Azure/azure-sdk-for-go/sdk/azcore/to"
	"github.com/Azure/azure-sdk-for-go/sdk/azidentity"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/containerservice/armcontainerservice"
	"github.com/Azure/azure-sdk-for-go/sdk/resourcemanager/resourcegraph/armresourcegraph"
	"github.com/gopuff/morecontext"
	"github.com/prometheus/client_golang/prometheus"
	"github.com/prometheus/client_golang/prometheus/promauto"
	"github.com/prometheus/client_golang/prometheus/promhttp"
	"github.com/sirupsen/logrus"
)

type Item struct {
	CurrencyCode string  `json:"currencyCode"`
	RetailPrice  float64 `json:"retailPrice"`
	SKUName      string  `json:"skuName"`
	ProductName  string  `json:"productName"`
}

type Response struct {
	Items []Item `json:"Items"`
}

type Nodepool struct {
	Name           string  `json:"name"`
	Discount       float64 `json:"discount"`
	EvictionRate   float64 `json:"evictionRate"`
	PlacementScore int     `json:"placementScore"`
	Version        int     `json:"version"`
	Type           string  `json:"type"`
}

type NodepoolMap map[string]Nodepool

type PlacementScoreCache struct {
	mu   sync.Mutex
	data placementCacheEntry
	ttl  time.Duration
}

type placementCacheEntry struct {
	cacheKey  string
	timestamp time.Time
	scores    map[string]map[string]int
}

var placementCache = &PlacementScoreCache{
	data: placementCacheEntry{
		cacheKey:  "",
		timestamp: time.Time{},
		scores:    make(map[string]map[string]int),
	},
	ttl: 15 * time.Minute, // minimum TTL per Azure docs
}

var (
	spotDiscountMetric = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "azure_spot_monitor_current_discount",
		Help: "The current effective spot discount from original VM price",
	}, []string{"region", "instance"})

	spotPriceMetric = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "azure_spot_monitor_spot_price",
		Help: "The current spot instance price",
	}, []string{"region", "instance"})

	spotRegularPriceMetric = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "azure_spot_monitor_regular_price",
		Help: "The original VM price",
	}, []string{"region", "instance"})

	spotPlacementScoreMetric = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "azure_spot_monitor_placement_score",
		Help: "The current placement score for the spot instance",
	}, []string{"region", "instance", "zone"})

	spotEvictionRateMetric = promauto.NewGaugeVec(prometheus.GaugeOpts{
		Name: "azure_spot_monitor_eviction_rate",
		Help: "The current spot instance eviciton rate",
	}, []string{"region", "instance"})
)

func getPrices(region, instance, baseURL string) (regular, spot float64, err error) {
	urlQuery := fmt.Sprintf("serviceName eq '%s' and priceType eq '%s' and armSkuName eq '%s' and armRegionName eq '%s'",
		"Virtual Machines",
		"Consumption",
		instance,
		region,
	)

	fullURL := fmt.Sprintf("%s?$filter=%s",
		baseURL,
		strings.ReplaceAll(url.QueryEscape(urlQuery), "+", "%20"),
	)

	resp, err := http.Get(fullURL)
	if err != nil {
		return 0, 0, err
	}
	lg.Info("fetching spot prices was successful")
	defer func(Body io.ReadCloser) {
		err := Body.Close()
		if err != nil {
			lg.WithError(err).Error("failed to close response body")
		}
	}(resp.Body)

	var data Response
	err = json.NewDecoder(resp.Body).Decode(&data)
	if err != nil {
		return 0, 0, err
	}

	var regularItem, spotItem Item

	for _, item := range data.Items {
		if strings.Contains(item.ProductName, "Windows") {
			continue
		} else if strings.Contains(item.SKUName, "Spot") {
			spotItem = item
		} else if strings.Contains(item.SKUName, "Low Priority") {
			// Skip low priority items
			continue
		} else {
			regularItem = item
		}
	}

	return regularItem.RetailPrice, spotItem.RetailPrice, nil

}

func getEvictionRates(region, instance string, ctx context.Context) (rate string, err error) {
	options := &azidentity.ManagedIdentityCredentialOptions{}

	// If a client ID is found, configure the options to use the specified user-assigned managed identity.
	clientID := os.Getenv("AZURE_CLIENT_ID")
	if clientID != "" {
		options.ID = azidentity.ClientID(clientID)
	}

	cred, err := azidentity.NewManagedIdentityCredential(options)
	if err != nil {
		return "", err
	}

	clientFactory, err := armresourcegraph.NewClientFactory(cred, nil)
	if err != nil {
		return "", err
	}

	query := fmt.Sprintf("spotresources | where type =~ 'microsoft.compute/skuspotevictionrate/location' | where sku.name == '%s' | where location == '%s' | project spotEvictionRate = properties.evictionRate", strings.ToLower(instance), region)

	res, err := clientFactory.NewClient().Resources(ctx, armresourcegraph.QueryRequest{
		Query: to.Ptr(query),
		Options: &armresourcegraph.QueryRequestOptions{
			ResultFormat: to.Ptr(armresourcegraph.ResultFormatObjectArray),
		},
	}, nil)
	if err != nil {
		return "", err
	}

	lg.Info("fetching eviction rate was successful")

	dataSlice, ok := res.Data.([]interface{})
	if !ok {
		return "", errors.New("query response type assertion failed")
	}
	//Eviction rates are (0-5%, 5-10%, 10-15%, 15-20%, 20+%)
	var spotEvictionRate string
	if len(dataSlice) > 0 {
		// Type assertion to access the map
		dataMap, ok := dataSlice[0].(map[string]interface{})
		if !ok {
			return "", errors.New("query response, failed to assert the first element of array as a map[string]interface{}")
		}
		// Accessing the value using the key
		spotEvictionRateRaw := dataMap["spotEvictionRate"].(string)
		parts := strings.Split(spotEvictionRateRaw, "-")
		if len(parts) > 1 {
			spotEvictionRate = parts[1]
		} else {
			spotEvictionRate = parts[0]
			if strings.Contains(spotEvictionRate, "+") {
				// Remove the '+' symbol
				spotEvictionRate = strings.ReplaceAll(spotEvictionRate, "+", "")
				// Increment the 20+ to 21 via string replacement
				spotEvictionRate = strings.ReplaceAll(spotEvictionRate, "0", "1")
			}
		}
	}

	return spotEvictionRate, nil
}

func getPlacementScores(region, subscriptionId string, clientID string, instances []string, ctx context.Context) (placementscores map[string]map[string]int, err error) {
	type skuObj struct {
		SKU string `json:"sku"`
	}

	type requestPayload struct {
		AvailabilityZones string   `json:"availabilityZones"`
		DesiredCount      string   `json:"desiredCount"`
		DesiredLocations  []string `json:"desiredLocations"`
		DesiredSizes      []skuObj `json:"desiredSizes"`
	}

	type placementScore struct {
		SKU              string `json:"sku"`
		AvailabilityZone string `json:"availabilityZone"`
		Score            string `json:"score"`
	}

	type responsePayload struct {
		PlacementScores []placementScore `json:"placementScores"`
	}

	scoreMap := map[string]int{
		"Low":    25,
		"Medium": 50,
		"High":   100,
	}

	sort.Strings(instances)
	cacheKey := fmt.Sprintf("%s-%s-%s", region, subscriptionId, strings.Join(instances, ","))
	placementCache.mu.Lock()
	entry := placementCache.data
	if entry.cacheKey == cacheKey && time.Since(entry.timestamp) < placementCache.ttl {
		lg.Info("returning placement scores from singleton cache")
		return entry.scores, nil
	}
	placementCache.mu.Unlock()

	options := &azidentity.ManagedIdentityCredentialOptions{}

	if clientID != "" {
		options.ID = azidentity.ClientID(clientID)
	}

	// Initialize Managed Identity credential
	cred, err := azidentity.NewManagedIdentityCredential(options)
	if err != nil {
		return nil, fmt.Errorf("failed to create managed identity credential: %w", err)
	}

	// Get access token for ARM
	scope := "https://management.azure.com/.default"
	token, err := cred.GetToken(ctx, policy.TokenRequestOptions{
		Scopes: []string{scope},
	})
	if err != nil {
		return nil, fmt.Errorf("failed to get access token: %w", err)
	}

	placementApiUrl := fmt.Sprintf(
		"https://management.azure.com/subscriptions/%s/providers/Microsoft.Compute/locations/%s/placementScores/spot/generate?api-version=2025-02-01-preview",
		subscriptionId,
		region,
	)

	result := make(map[string]map[string]int)

	// Break instances into chunks of 5
	for i := 0; i < len(instances); i += 5 {
		end := i + 5
		if end > len(instances) {
			end = len(instances)
		}
		chunk := instances[i:end]

		var sizes []skuObj
		for _, sku := range chunk {
			sizes = append(sizes, skuObj{SKU: sku})
		}

		payload := requestPayload{
			AvailabilityZones: "true",
			DesiredCount:      "1",
			DesiredLocations:  []string{region},
			DesiredSizes:      sizes,
		}

		body, err := json.Marshal(payload)
		if err != nil {
			return nil, fmt.Errorf("failed to marshal request payload: %w", err)
		}

		// Create and send request
		var resp *http.Response
		maxRetries := 3
		retryDelay := time.Minute

		for attempt := 0; attempt < maxRetries; attempt++ {
			req, err := http.NewRequest("POST", placementApiUrl, bytes.NewBuffer(body))
			if err != nil {
				return nil, fmt.Errorf("failed to create request: %w", err)
			}
			req.Header.Set("Content-Type", "application/json")
			req.Header.Set("Authorization", "Bearer "+token.Token)

			resp, err = http.DefaultClient.Do(req)
			if err != nil {
				return nil, fmt.Errorf("request failed: %w", err)
			}

			if resp.StatusCode == http.StatusTooManyRequests {
				// Backoff and retry
				lg.Warnf("Received 429 Too Many Requests. Retrying in %s...", retryDelay)
				resp.Body.Close()
				time.Sleep(retryDelay)
				retryDelay *= 4 // Exponential backoff
				continue
			}

			// Break out if it's not a 429
			break
		}

		defer resp.Body.Close()
		if resp.StatusCode >= 400 {
			return nil, fmt.Errorf("API error: %s", resp.Status)
		}
		lg.Infof("fetching placementscore chunk %v was successful", chunk)

		var parsed responsePayload
		if err := json.NewDecoder(resp.Body).Decode(&parsed); err != nil {
			return nil, fmt.Errorf("failed to decode response: %w", err)
		}

		for _, ps := range parsed.PlacementScores {
			if _, ok := result[ps.SKU]; !ok {
				result[ps.SKU] = make(map[string]int)
			}
			result[ps.SKU][ps.AvailabilityZone] = scoreMap[ps.Score]
		}
	}

	placementCache.mu.Lock()
	placementCache.data = placementCacheEntry{
		cacheKey:  cacheKey,
		timestamp: time.Now(),
		scores:    result,
	}
	placementCache.mu.Unlock()

	lg.Info("placement scores calculated and cached", "result", result)

	return result, nil
}

func getNodepools(subscriptionId string, resourceGroup string, clientID string, cluster string, ctx context.Context) (region string, instances map[string][]map[string]string, err error) {
	instanceTypes := make(map[string][]map[string]string)

	options := &azidentity.ManagedIdentityCredentialOptions{}

	// If a client ID is found, configure the options to use the specified user-assigned managed identity.
	if clientID != "" {
		options.ID = azidentity.ClientID(clientID)
	}

	cred, err := azidentity.NewManagedIdentityCredential(options)
	if err != nil {
		lg.WithError(err).Fatal("Failed to obtain Azure credentials")
		return "", instanceTypes, err
	}

	//Get region from AKS cluster
	aksClient, err := armcontainerservice.NewManagedClustersClient(subscriptionId, cred, nil)
	if err != nil {
		lg.WithError(err).Fatal("Failed to create AKS client")
		return "", instanceTypes, err
	}

	aksResp, err := aksClient.Get(context.Background(), resourceGroup, cluster, nil)
	if err != nil {
		lg.WithError(err).Fatal("Failed to get AKS cluster")
		return "", instanceTypes, err
	}
	region = *aksResp.Location

	// Create a Node Pool client
	client, err := armcontainerservice.NewAgentPoolsClient(subscriptionId, cred, nil)
	if err != nil {
		lg.WithError(err).Fatal("Failed to create AKS AgentPools client")
		return "", instanceTypes, err
	}

	// Get the list of node pools
	pager := client.NewListPager(resourceGroup, cluster, nil)

	for pager.More() {
		resp, err := pager.NextPage(ctx)
		if err != nil {
			lg.WithError(err).Fatal("Failed to get node pools")
			return "", instanceTypes, err
		}

		for _, np := range resp.Value {
			if np.Properties.ProvisioningState == nil || *np.Properties.ProvisioningState != "Succeeded" {
				continue
			}
			if np.Properties != nil && np.Properties.VMSize != nil && np.Name != nil {
				vmSize := *np.Properties.VMSize
				nodePoolName := *np.Name
				props := map[string]string{
					"name": nodePoolName,
				}

				/**
				Select one zone for nodepools with more than one zone assigned,
				as the request for a VM will still pick an arbitrary zone
				**/
				if len(np.Properties.AvailabilityZones) > 0 {
					props["zone"] = *np.Properties.AvailabilityZones[0]
				}
				if np.Properties.ScaleSetPriority != nil && *np.Properties.ScaleSetPriority == "Spot" {
					props["priority"] = "Spot"
				} else {
					if np.Properties.Mode != nil && *np.Properties.Mode == "System" {
						continue
					}
					props["priority"] = "Regular"
				}
				instanceTypes[vmSize] = append(instanceTypes[vmSize], props)
			}
		}
	}

	return region, instanceTypes, nil
}

func main() {
	cfg := setupConfig()
	ctx := morecontext.ForSignals()
	levelStr := cfg.GetString("logging.level")
	logLevel, err := logrus.ParseLevel(levelStr)
	if err == nil {
		lg.SetLevel(logLevel)
	}
	versionRegexp := regexp.MustCompile("[0-9]+$")

	http.Handle("/metrics", promhttp.Handler())

	go func() {
		err := http.ListenAndServe(cfg.GetString("metrics.addr"), nil)
		if err != nil {
			lg.WithError(err).Error("failed to start prometheus listener")
			panic(err)
		}
	}()
	lg.Info("started prometheus listener")

	ticker := time.NewTicker(time.Second * time.Duration(cfg.GetInt("time.interval")))

	subscriptionId := cfg.GetString("subscription.id")
	if subscriptionId == "" {
		lg.Fatal("Missing required config: subscription.id")
	}
	resourceGroup := cfg.GetString("resource.group")
	if resourceGroup == "" {
		lg.Fatal("Missing required config: resource.group")
	}
	clusterName := cfg.GetString("cluster.name")
	if clusterName == "" {
		lg.Fatal("Missing required config: cluster.name")
	}
	clientId := cfg.GetString("azure.client.id")
	if clientId == "" {
		lg.Fatal("Missing required config: azure.client.id")
	}

	for {
		region, instances, err := getNodepools(subscriptionId, resourceGroup, clientId, clusterName, ctx)
		if err != nil {
			lg.WithError(err).Error("Failed to get nodepools")
			return
		}

		instanceKeys := make([]string, 0, len(instances))
		for key := range instances {
			instanceKeys = append(instanceKeys, key)
		}

		placementscores, err := getPlacementScores(region, subscriptionId, clientId, instanceKeys, ctx)
		if err != nil {
			lg.WithError(err).Error("Failed to get placement scores")
			return
		}

		nodePools := make(NodepoolMap)

		select {
		case <-ctx.Done():
			return
		case <-ticker.C:

			for instance, nodepool := range instances {
				lg.Infof("fetching current spot prices for %s", instance)
				regularPrice, spotPrice, err := getPrices(region, instance, cfg.GetString("api.url"))
				if err != nil {
					lg.WithError(err).Error("Failed to get prices")
					continue
				}

				lg.Infof("fetching current eviction rates for %s", instance)
				evictionRateStr, err := getEvictionRates(region, instance, ctx)
				if err != nil {
					lg.WithError(err).Error("Failed to get eviction rate")
					continue
				}

				evictionRate, err := strconv.Atoi(evictionRateStr)
				if err != nil && evictionRateStr == "" {
					evictionRate = 0
				} else if err != nil {
					lg.WithError(err).Error("Failed to convert eviction rate to int")
					continue
				}
				percentDiscount := ((regularPrice - spotPrice) / regularPrice)
				spotPriceMetric.WithLabelValues(region, instance).Set(spotPrice)
				spotRegularPriceMetric.WithLabelValues(region, instance).Set(regularPrice)
				spotDiscountMetric.WithLabelValues(region, instance).Set(percentDiscount)
				spotEvictionRateMetric.WithLabelValues(region, instance).Set(float64(evictionRate) / 100)

				version := 1
				versionString := versionRegexp.FindString(instance)
				if versionString != "" {
					version, _ = strconv.Atoi(versionString)
				}

				for _, node := range nodepool {
					if node["priority"] == "Spot" {
						nodePool := Nodepool{
							Name:           node["name"],
							Discount:       percentDiscount,
							EvictionRate:   float64(evictionRate) / 100,
							PlacementScore: placementscores[instance][node["zone"]],
							Version:        version,
							Type:           "Spot",
						}
						// Append the Nodepool object to the corresponding slice
						nodePools[node["name"]] = nodePool
						spotPlacementScoreMetric.WithLabelValues(region, instance, node["zone"]).Set(float64(placementscores[instance][node["zone"]]))
					} else {
						nodePool := Nodepool{
							Name:           node["name"],
							Discount:       0.5,
							EvictionRate:   0.2,
							PlacementScore: 45,
							Version:        2,
							Type:           "Regular",
						}
						nodePools[node["name"]] = nodePool
					}
				}
			}

			err = updateConfigMap(ctx, cfg, nodePools)
			if err != nil {
				lg.WithError(err).Error("Failed to update configmap")
				continue
			}

		}
	}
}
