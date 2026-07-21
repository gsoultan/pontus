import { createClient } from "@connectrpc/connect";
import { ManagementService } from "../../gen/api/proto/service/management_pb";
import { transport } from "../../services/transport";

export const statusClient = createClient(ManagementService, transport);
