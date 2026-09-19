package pluginsdk

// PermissionNetworkFull grants an RPC plugin ambient access to the Host's
// network namespace, including unrestricted INET listen and outbound sockets.
// Hosts must keep plugins without this explicit permission network-isolated.
const PermissionNetworkFull = "network.full"
