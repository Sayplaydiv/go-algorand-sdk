package algod

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"io/ioutil"
	"net/http"
	"net/url"
	"strings"

	"github.com/google/go-querystring/query"

	"github.com/algorand/go-algorand-sdk/client/algod/models"
	"github.com/algorand/go-algorand-sdk/encoding/msgpack"
	"github.com/algorand/go-algorand-sdk/types"
)

const (
	authHeader           = "X-Algo-API-Token"
	healthCheckEndpoint  = "/health"
	apiVersionPathPrefix = "/v1"
)

// unversionedPaths ais a set of paths that should not be prefixed by the API version
var unversionedPaths = map[string]bool{
	"/versions": true,
	"/health":   true,
}

// rawRequestPaths is a set of paths where the body should not be urlencoded
var rawRequestPaths = map[string]bool{
	"/transactions": true,
}

// Client manages the REST interface for a calling user.
type Client struct {
	serverURL url.URL
	apiToken  string
}

// MakeClient is the factory for constructing a Client for a given endpoint
func MakeClient(address string, apiToken string) (c Client, err error) {
	url, err := url.Parse(address)
	if err != nil {
		return
	}
	c = Client{
		serverURL: *url,
		apiToken:  apiToken,
	}
	return
}

// extractError checks if the response signifies an error (for now, StatusCode != 200).
// If so, it returns the error.
// Otherwise, it returns nil.
func extractError(resp *http.Response) error {
	if resp.StatusCode == 200 {
		return nil
	}

	errorBuf, _ := ioutil.ReadAll(resp.Body) // ignore returned error
	return fmt.Errorf("HTTP %v: %s", resp.Status, errorBuf)
}

// stripTransaction gets a transaction of the form "tx-XXXXXXXX" and truncates the "tx-" part, if it starts with "tx-"
func stripTransaction(tx string) string {
	if strings.HasPrefix(tx, "tx-") {
		return strings.SplitAfter(tx, "-")[1]
	}
	return tx
}

// submitForm is a helper used for submitting (ex.) GETs and POSTs to the server
func (client Client) submitForm(response interface{}, path string, request interface{}, requestMethod string, encodeJSON bool) error {
	var err error
	queryURL := client.serverURL
	queryURL.Path = path

	// Handle version prefix
	if !unversionedPaths[path] {
		queryURL.Path = strings.Join([]string{apiVersionPathPrefix, path}, "")
	}

	var req *http.Request
	var body io.Reader

	if request != nil {
		if rawRequestPaths[path] {
			reqBytes, ok := request.([]byte)
			if !ok {
				return fmt.Errorf("couldn't decode raw request as bytes")
			}
			body = bytes.NewBuffer(reqBytes)
		} else {
			v, err := query.Values(request)
			if err != nil {
				return err
			}

			queryURL.RawQuery = v.Encode()
			if encodeJSON {
				jsonValue, _ := json.Marshal(request)
				body = bytes.NewBuffer(jsonValue)
			}
		}
	}

	req, err = http.NewRequest(requestMethod, queryURL.String(), body)
	if err != nil {
		return err
	}

	// If we add another endpoint that does not require auth, we should add a
	// requiresAuth argument to submitForm rather than checking here
	if path != healthCheckEndpoint {
		req.Header.Set(authHeader, client.apiToken)
	}

	httpClient := &http.Client{}
	resp, err := httpClient.Do(req)

	if err != nil {
		return err
	}

	defer resp.Body.Close()

	err = extractError(resp)
	if err != nil {
		return err
	}

	dec := json.NewDecoder(resp.Body)
	return dec.Decode(&response)
}

// get performs a GET request to the specific path against the server
func (client Client) get(response interface{}, path string, request interface{}) error {
	return client.submitForm(response, path, request, "GET", false /* encodeJSON */)
}

// post sends a POST request to the given path with the given request object.
// No query parameters will be sent if request is nil.
// response must be a pointer to an object as post writes the response there.
func (client Client) post(response interface{}, path string, request interface{}) error {
	return client.submitForm(response, path, request, "POST", true /* encodeJSON */)
}

// as post, but with MethodPut
func (client Client) put(response interface{}, path string, request interface{}) error {
	return client.submitForm(response, path, request, "PUT", true /* encodeJSON */)
}

// as post, but with MethodPatch
func (client Client) patch(response interface{}, path string, request interface{}) error {
	return client.submitForm(response, path, request, "PATCH", true /* encodeJSON */)
}

// Status retrieves the StatusResponse from the running node
// the StatusResponse includes data like the consensus version and current round
// Not supported
func (client Client) Status() (response models.NodeStatus, err error) {
	err = client.get(&response, "/status", nil)
	return
}

// HealthCheck does a health check on the the potentially running node,
// returning an error if the API is down
func (client Client) HealthCheck() error {
	return client.get(nil, "/health", nil)
}

// StatusAfterBlock waits for a block to occur then returns the StatusResponse after that block
// blocks on the node end
// Not supported
func (client Client) StatusAfterBlock(blockNum uint64) (response models.NodeStatus, err error) {
	err = client.get(&response, fmt.Sprintf("/status/wait-for-block-after/%d", blockNum), nil)
	return
}

type pendingTransactionsParams struct {
	Max uint64 `url:"max"`
}

// GetPendingTransactions asks algod for a snapshot of current pending txns on the node, bounded by maxTxns.
// If maxTxns = 0, fetches as many transactions as possible.
func (client Client) GetPendingTransactions(maxTxns uint64) (response models.PendingTransactions, err error) {
	err = client.get(&response, fmt.Sprintf("/transactions/pending"), pendingTransactionsParams{maxTxns})
	return
}

// Versions retrieves the VersionResponse from the running node
// the VersionResponse includes data like version number and genesis ID
func (client Client) Versions() (response models.Version, err error) {
	err = client.get(&response, "/versions", nil)
	return
}

// LedgerBalances gets the BalancesResponse for the specified node // TODO: To be removed before production
func (client Client) LedgerBalances() (response models.Balances, err error) {
	err = client.get(&response, "/ledger/balances", nil)
	return
}

// LedgerSupply gets the supply details for the specified node's Ledger
func (client Client) LedgerSupply() (response models.Supply, err error) {
	err = client.get(&response, "/ledger/supply", nil)
	return
}

type transactionsByAddrParams struct {
	FirstRound uint64 `url:"firstRound"`
	LastRound  uint64 `url:"lastRound"`
}

// TransactionsByAddr returns all transactions for a PK [addr] in the [first,
// last] rounds range.
func (client Client) TransactionsByAddr(addr string, first, last uint64) (response models.TransactionList, err error) {
	err = client.get(&response, fmt.Sprintf("/account/%s/transactions", addr), transactionsByAddrParams{first, last})
	return
}

// AccountInformation also gets the AccountInformationResponse associated with the passed address
func (client Client) AccountInformation(address string) (response models.Account, err error) {
	err = client.get(&response, fmt.Sprintf("/account/%s", address), nil)
	return
}

// TransactionInformation gets information about a specific transaction involving a specific account
// it will only return information about transactions submitted to the node queried
func (client Client) TransactionInformation(accountAddress, transactionID string) (response models.Transaction, err error) {
	transactionID = stripTransaction(transactionID)
	err = client.get(&response, fmt.Sprintf("/account/%s/transaction/%s", accountAddress, transactionID), nil)
	return
}

// SuggestedFee gets the recommended transaction fee from the node
func (client Client) SuggestedFee() (response models.TransactionFee, err error) {
	err = client.get(&response, "/transactions/fee", nil)
	return
}

// SendRawTransaction gets a SignedTxn and broadcasts it to the network
func (client Client) SendRawTransaction(txn types.SignedTxn) (response models.TransactionID, err error) {
	err = client.post(&response, "/transactions", msgpack.Encode(txn))
	return
}

// Block gets the block info for the given round
func (client Client) Block(round uint64) (response models.Block, err error) {
	err = client.get(&response, fmt.Sprintf("/block/%d", round), nil)
	return
}

// GetGoRoutines gets a dump of the goroutines from pprof
// Not supported
func (client Client) GetGoRoutines(ctx context.Context) (goRoutines string, err error) {
	// issue a "/debug/pprof/goroutine?debug=1" request
	query := make(map[string]string)
	query["debug"] = "1"

	goRoutines, err = client.doGetWithQuery(ctx, "/debug/pprof/goroutine", query)
	return
}

func (client Client) doGetWithQuery(ctx context.Context, path string, queryArgs map[string]string) (result string, err error) {
	queryURL := client.serverURL
	queryURL.Path = path

	req, err := http.NewRequest("GET", queryURL.String(), nil)
	if err != nil {
		return
	}
	q := req.URL.Query()
	for k, v := range queryArgs {
		q.Add(k, v)
	}
	req.URL.RawQuery = q.Encode()

	req.Header.Set(authHeader, client.apiToken)

	httpClient := http.Client{}
	resp, err := httpClient.Do(req.WithContext(ctx))
	if err != nil {
		return
	}
	defer resp.Body.Close()

	err = extractError(resp)
	if err != nil {
		return
	}

	bytes, err := ioutil.ReadAll(resp.Body)
	if err != nil {
		return
	}
	result = string(bytes)
	return
}