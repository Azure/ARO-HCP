// Copyright 2026 Microsoft Corporation
//
// Licensed under the Apache License, Version 2.0 (the "License");
// you may not use this file except in compliance with the License.
// You may obtain a copy of the License at
//
//     http://www.apache.org/licenses/LICENSE-2.0
//
// Unless required by applicable law or agreed to in writing, software
// distributed under the License is distributed on an "AS IS" BASIS,
// WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
// See the License for the specific language governing permissions and
// limitations under the License.

// The controllers in the operationcontrollers package provide the glue between
// Clusters Service and asynchronous operations initiated by RP frontend pods in
// response to client requests.
//
// For background reading about Azure's asynchronous operation contract for
// Resource Providers, see the [Resource Provider Contract].
//
// The ARO-HCP RP uses type api.Operation to represent an asynchronous operation.
// These structs get converted to JSON format and stored in Cosmos DB as so-called
// "operation documents".
//
// At the time of this writing the RP backend defers most of the actual work involved
// in an operation to Clusters Service. The controllers in this package merely update
// operation documents in Cosmos DB to reflect the status of the actual operation in
// Clusters Service.
//
// Generally speaking, the lifecycle of an operation document is as follows:
//
//  1. A frontend pod creates the operation document in Cosmos DB before responding
//     to the client requesting the operation.
//
//  2. On the backend, the new operation document is first noticed by a "dispatch
//     controller" that is dedicated to the operation's particular request type and
//     resource type, such as "create cluster" or "delete node pool". The dispatch
//     controller makes the appropriate calls to dispatch the operation to Clusters
//     Service.
//
//  3. Once the operation is dispatched to Clusters Service, an "operation controller"
//     begins polling the Clusters Service resource associated with the operation for
//     status changes, and updates the operation document in Cosmos DB accordingly.
//
//  4. Meanwhile, the frontend will have exposed a status endpoint for this operation
//     for the client to poll. (The endpoint is returned to the client as a header in
//     the initial response.) The response body format of this endpoint is defined by
//     Azure, but the operation document in Cosmos DB has all the required details to
//     build a compliant response.
//
//  5. Operation documents in Cosmos DB are transient by way of a time-to-live (TTL)
//     value. Once this TTL period (currently 7 days) expires, the Cosmos DB service
//     will automatically delete the operation document.
//
// [Resource Provider Contract]: https://github.com/cloud-and-ai-microsoft/resource-provider-contract/blob/master/v1.0/async-api-reference.md
package operationcontrollers
