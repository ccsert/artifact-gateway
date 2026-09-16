import type { PublicRepositoryFormat } from "../../lib/publicRepositoryUsage";

export interface PublicRepository {
  id: string;
  name: string;
  format: PublicRepositoryFormat;
  type?: string;
}
