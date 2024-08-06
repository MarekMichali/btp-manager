package api_test

import (
	"bytes"
	"context"
	"encoding/json"
	"log/slog"
	"net/http"
	"testing"
	"time"

	"github.com/kyma-project/btp-manager/internal/api"
	"github.com/kyma-project/btp-manager/internal/api/requests"
	"github.com/kyma-project/btp-manager/internal/api/responses"
	clusterobject "github.com/kyma-project/btp-manager/internal/cluster-object"
	servicemanager "github.com/kyma-project/btp-manager/internal/service-manager"
	"github.com/kyma-project/btp-manager/internal/service-manager/types"
	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

const (
	port         = "8080"
	readTimeout  = 1 * time.Second
	writeTimeout = 1 * time.Second
	idleTimeout  = 2 * time.Second
)

func TestAPI(t *testing.T) {
	// before all
	cfg := api.Config{
		Port:         port,
		ReadTimeout:  readTimeout,
		WriteTimeout: writeTimeout,
		IdleTimeout:  idleTimeout,
	}
	defaultSIs := defaultServiceInstances()
	defaultSBs := defaultServiceBindings()

	fakeSM, err := servicemanager.NewFakeServer()
	require.NoError(t, err)

	fakeSM.Start()
	defer fakeSM.Close()
	httpClient := fakeSM.Client()
	url := fakeSM.URL

	secretMgr := clusterobject.NewFakeSecretManager()
	err = secretMgr.Create(context.TODO(), clusterobject.FakeDefaultSecret())
	require.NoError(t, err)

	fakeSMClient := servicemanager.NewClient(context.TODO(), slog.Default(), secretMgr)
	fakeSMClient.SetHTTPClient(httpClient)
	fakeSMClient.SetSMURL(url)

	btpMgrAPI := api.NewAPI(cfg, fakeSMClient, secretMgr, nil)
	apiAddr := "http://localhost" + btpMgrAPI.Address()
	go btpMgrAPI.Start()

	apiClient := http.Client{
		Timeout: 500 * time.Millisecond,
	}

	t.Run("GET Service Instances", func(t *testing.T) {
		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances", nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()

		var sis responses.ServiceInstances
		err = json.NewDecoder(resp.Body).Decode(&sis)
		require.NoError(t, err)

		// then
		assert.Equal(t, sis.NumItems, 4)
		assert.ElementsMatch(t, sis.Items, defaultSIs.Items)
	})

	t.Run("GET Service Instance by ID", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"
		expectedSI := getServiceInstanceByID(defaultSIs, siID)
		expectedSI.ServicePlanID = "4036790e-5ef3-4cf7-bb16-476053477a9a"
		expectedSI.ServicePlanName = "service1-plan2"

		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances/"+siID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()

		var si responses.ServiceInstance
		err = json.NewDecoder(resp.Body).Decode(&si)
		require.NoError(t, err)

		// then
		assert.Equal(t, expectedSI, si)
	})

	t.Run("POST Service Instance", func(t *testing.T) {
		// given
		siToCreate := requests.CreateServiceInstance{
			Name:          "new-si-01",
			ServicePlanID: "4036790e-5ef3-4cf7-bb16-476053477a9a",
			Namespace:     "kyma-system",
			ClusterID:     "test-cluster-id",
			Labels: map[string][]string{
				"label1": {"value1"},
				"label2": {"value2a", "value2b"},
			},
			Parameters: []byte(`{"param1": "value1", "param2": "value2"}`),
		}
		siToCreateJSON, err := json.Marshal(siToCreate)
		require.NoError(t, err)

		expectedSI := responses.ServiceInstance{
			Name:          "new-si-01",
			Namespace:     "kyma-system",
			ServicePlanID: "4036790e-5ef3-4cf7-bb16-476053477a9a",
			ClusterID:     "test-cluster-id",
		}

		// when
		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-instances", bytes.NewBuffer(siToCreateJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()

		var si responses.ServiceInstance
		err = json.NewDecoder(resp.Body).Decode(&si)
		require.NoError(t, err)

		// then
		assert.NotEmpty(t, si.ID)
		assert.NotEmpty(t, si.SubaccountID)
		expectedSI.ID = si.ID                     // ID is generated by the server
		expectedSI.SubaccountID = si.SubaccountID // SubaccountID is determined from the secret
		assert.Equal(t, expectedSI, si)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("PATCH Service Instance by ID", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"
		expectedSI := getServiceInstanceByID(defaultSIs, siID)
		expectedSI.Name = "a7e240d6-updated-si"
		expectedSI.ServicePlanID = "61772d7e-4e67-48f5-90fc-dd9254aa454b"
		expectedSI.ServicePlanName = "service3-plan1"

		siToUpdate := types.ServiceInstanceUpdateRequest{
			Name:          &expectedSI.Name,
			ServicePlanID: &expectedSI.ServicePlanID,
		}

		siToUpdateJSON, err := json.Marshal(siToUpdate)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodPatch, apiAddr+"/api/service-instances/"+siID, bytes.NewBuffer(siToUpdateJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()

		var si responses.ServiceInstance
		err = json.NewDecoder(resp.Body).Decode(&si)
		require.NoError(t, err)

		// then
		assert.Equal(t, expectedSI.Name, si.Name)
		assert.Equal(t, expectedSI.ServicePlanID, si.ServicePlanID)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("DELETE Service Instance by ID", func(t *testing.T) {
		// given
		siID := "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6"

		// when
		req, err := http.NewRequest(http.MethodDelete, apiAddr+"/api/service-instances/"+siID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)

		// then
		req, err = http.NewRequest(http.MethodGet, apiAddr+"/api/service-instances/"+siID, nil)
		resp, err = apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 500, resp.StatusCode) // change to 404 after error handling refactoring

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("GET Service Bindings", func(t *testing.T) {
		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings", nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()

		var sbs responses.ServiceBindings
		err = json.NewDecoder(resp.Body).Decode(&sbs)
		require.NoError(t, err)

		// then
		assert.Equal(t, sbs.NumItems, 4)
		assert.ElementsMatch(t, sbs.Items, defaultSBs.Items)
	})

	t.Run("GET Service Binding by ID", func(t *testing.T) {
		// given
		sbID := "318a16c3-7c80-485f-b55c-918629012c9a"
		expectedSI := getServiceBindingByID(defaultSBs, sbID)

		// when
		req, err := http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings/"+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, 200, resp.StatusCode)
		defer resp.Body.Close()

		var sb responses.ServiceBinding
		err = json.NewDecoder(resp.Body).Decode(&sb)
		require.NoError(t, err)

		// then
		assert.Equal(t, expectedSI, sb)
	})

	t.Run("POST Service Binding", func(t *testing.T) {
		// given
		sbCreateRequest := requests.CreateServiceBinding{
			Name:              "sb-test-01",
			ServiceInstanceID: "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
			Parameters:        []byte(`{"param1": "value1", "param2": "value2"}`),
		}
		sbCreateRequestJSON, err := json.Marshal(sbCreateRequest)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodPost, apiAddr+"/api/service-bindings", bytes.NewBuffer(sbCreateRequestJSON))
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		require.Equal(t, http.StatusOK, resp.StatusCode) // change expected status code to 201 after error handling refactoring
		defer resp.Body.Close()

		var sb responses.ServiceBinding
		err = json.NewDecoder(resp.Body).Decode(&sb)
		require.NoError(t, err)

		// then
		assert.NotEmpty(t, sb.ID)
		assert.Equal(t, sbCreateRequest.Name, sb.Name)

		// when
		secrets, err := secretMgr.GetAllByLabels(context.TODO(), map[string]string{
			clusterobject.ServiceBindingIDLabel:  sb.ID,
			clusterobject.ServiceInstanceIDLabel: sbCreateRequest.ServiceInstanceID,
		})
		require.NoError(t, err)

		// then
		assert.Len(t, secrets.Items, 1)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})

	t.Run("DELETE Service Binding by ID", func(t *testing.T) {
		// given
		sbID := "318a16c3-7c80-485f-b55c-918629012c9a"
		labels := map[string]string{
			clusterobject.ManagedByLabelKey:     clusterobject.OperatorName,
			clusterobject.ServiceBindingIDLabel: sbID,
		}
		secretToDelete := &corev1.Secret{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "service-binding-3-secret",
				Namespace: "kyma-system",
				Labels:    labels,
			},
			StringData: map[string]string{"username": "user3", "password": "pass3"},
		}
		err := secretMgr.Create(context.TODO(), secretToDelete)
		require.NoError(t, err)

		// when
		req, err := http.NewRequest(http.MethodDelete, apiAddr+"/api/service-bindings/"+sbID, nil)
		resp, err := apiClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// then
		require.Equal(t, http.StatusOK, resp.StatusCode)

		// when
		req, err = http.NewRequest(http.MethodGet, apiAddr+"/api/service-bindings/"+sbID, nil)
		resp, err = apiClient.Do(req)
		require.NoError(t, err)
		defer resp.Body.Close()

		// then
		require.Equal(t, http.StatusInternalServerError, resp.StatusCode) // change to 404 after error handling refactoring

		// when
		secrets, err := secretMgr.GetAllByLabels(context.TODO(), labels)
		require.NoError(t, err)

		// then
		assert.Len(t, secrets.Items, 0)

		err = fakeSM.RestoreDefaults()
		require.NoError(t, err)
	})
}

func defaultServiceInstances() responses.ServiceInstances {
	return responses.ServiceInstances{
		NumItems: 4,
		Items: []responses.ServiceInstance{
			{
				ID:           "f9ffbaa4-739a-4a16-ad02-6f2f17a830c5",
				Name:         "si-test-1",
				Namespace:    "kyma-system",
				SubaccountID: "a4bdee5b-2bc4-4a44-915b-196ae18c7f29",
				ClusterID:    "59c7efc0-d6bc-4d07-87cf-9bd049534afe",
			},
			{
				ID:           "df28885c-7c5f-46f0-bb75-0ae2dc85ac41",
				Name:         "si-test-2",
				Namespace:    "kyma-system",
				SubaccountID: "5ef574ba-5fb3-493f-839c-48b787f2b710",
				ClusterID:    "5dc40d3c-1839-4173-9743-d5b4f36d9d7b",
			},
			{
				ID:           "a7e240d6-e348-4fc0-a54c-7b7bfe9b9da6",
				Name:         "si-test-3",
				Namespace:    "kyma-system",
				SubaccountID: "73b7f0df-6376-4115-8e45-a0e005c0f5d2",
				ClusterID:    "4f6ee6a5-9c28-4e50-8b91-708345e1b607",
			},
			{
				ID:           "c7a604e8-f289-4f61-841f-c6519db8daf2",
				Name:         "si-test-4",
				Namespace:    "kyma-system",
				SubaccountID: "ad4e88f7-e9cc-4346-944a-d9e0dc42a038",
				ClusterID:    "8e0b4ad1-4fa0-4f7f-a6a7-3db2ac0779e2",
			},
		},
	}
}

func getServiceInstanceByID(serviceInstances responses.ServiceInstances, serviceInstanceID string) responses.ServiceInstance {
	for _, si := range serviceInstances.Items {
		if si.ID == serviceInstanceID {
			return si
		}
	}
	return responses.ServiceInstance{}
}

func defaultServiceBindings() responses.ServiceBindings {
	return responses.ServiceBindings{
		NumItems: 4,
		Items: []responses.ServiceBinding{
			{
				ID:          "550e8400-e29b-41d4-a716-446655440003",
				Name:        "service-binding",
				Credentials: map[string]interface{}{"username": "user", "password": "pass"},
			},
			{
				ID:          "9e420bca-4cf2-4858-ade2-e5ef23cd756f",
				Name:        "service-binding-2",
				Credentials: map[string]interface{}{"username": "user2", "password": "pass2"},
			},
			{
				ID:          "318a16c3-7c80-485f-b55c-918629012c9a",
				Name:        "service-binding-3",
				Credentials: map[string]interface{}{"username": "user3", "password": "pass3"},
			},
			{
				ID:          "8e97d56b-9fc1-43db-9d2e-e52f8ce91046",
				Name:        "service-binding-4",
				Credentials: map[string]interface{}{"username": "user3", "password": "pass3"},
			},
		},
	}
}

func getServiceBindingByID(serviceBinding responses.ServiceBindings, serviceBindingID string) responses.ServiceBinding {
	for _, sb := range serviceBinding.Items {
		if sb.ID == serviceBindingID {
			return sb
		}
	}
	return responses.ServiceBinding{}
}