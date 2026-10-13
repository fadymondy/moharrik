// Names for the API's shapes, generated from the Go handlers (openapi.d.ts; `npm run gen:types`).
// Never hand-write a response type: alias the generated schema so a server change fails typecheck.
import type { components } from "./openapi"

type S = components["schemas"]

export type Workspace = S["WorkspaceOut"]
export type Conversation = S["ConversationOut"]
export type ConversationList = S["ConversationList"]
export type ConversationDetail = S["ConversationDetail"]
export type ConversationPatch = S["ConversationPatch"]
export type OrganisePatch = S["OrganisePatch"]
export type Message = S["MessageOut"]
export type ThreadPage = S["ThreadPage"]
export type Label = S["Label"]
export type LabelRef = S["LabelRef"]
export type Member = S["MemberOut"]
export type Bell = S["Bell"]
export type BellItem = S["BellItem"]
export type NotificationItem = S["NotificationItemOut"]
export type NotificationList = S["ItemList"]
export type Contact = S["ContactOut"]
export type ContactPage = S["ContactPage"]
export type ContactTimeline = S["ContactTimelineOut"]
export type ContactEvent = S["ContactEvent"]
export type Company = S["CompanyPage"] extends { companies?: (infer C)[] | null } ? C : never
export type CompanyPage = S["CompanyPage"]
export type Deal = S["DealOut"]
export type DealsPage = S["DealsPage"]
export type Stage = S["StageOut"]
export type DealFields = S["DealFields"]
export type PushPrefs = S["Prefs"]
export type AppDevice = S["AppDevice"]
export type RegisterDevice = S["Register-my-app-deviceRequest"]
