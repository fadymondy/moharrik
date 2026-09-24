/*
A preset's icon (MH-478). The library names lucide icons the way fadymondy.com
stored them — "Newspaper", "mail-open", "message-square" — so the name is
normalised and looked up in a fixed map: importing every lucide icon to look
one up by name would ship all of them. An unknown name gets the workflow icon.
*/

import {
  AtSignIcon,
  BellIcon,
  BriefcaseIcon,
  ChartLineIcon,
  CloudIcon,
  FacebookIcon,
  FileTextIcon,
  FlaskConicalIcon,
  ImagePlusIcon,
  InboxIcon,
  InstagramIcon,
  LinkedinIcon,
  ListIcon,
  MailOpenIcon,
  MessageCircleIcon,
  MessageSquareIcon,
  Music2Icon,
  NewspaperIcon,
  ScrollTextIcon,
  SearchIcon,
  SendIcon,
  Share2Icon,
  SquarePenIcon,
  TrendingUpIcon,
  TwitterIcon,
  UserIcon,
  UsersIcon,
  VideoIcon,
  WorkflowIcon,
  YoutubeIcon,
  type LucideIcon,
} from "lucide-react"

const ICONS: Record<string, LucideIcon> = {
  atsign: AtSignIcon,
  bell: BellIcon,
  briefcase: BriefcaseIcon,
  chartline: ChartLineIcon,
  cloud: CloudIcon,
  facebook: FacebookIcon,
  filetext: FileTextIcon,
  flaskconical: FlaskConicalIcon,
  imageplus: ImagePlusIcon,
  inbox: InboxIcon,
  instagram: InstagramIcon,
  linkedin: LinkedinIcon,
  list: ListIcon,
  mailopen: MailOpenIcon,
  messagecircle: MessageCircleIcon,
  messagesquare: MessageSquareIcon,
  music2: Music2Icon,
  newspaper: NewspaperIcon,
  scrolltext: ScrollTextIcon,
  search: SearchIcon,
  send: SendIcon,
  share2: Share2Icon,
  squarepen: SquarePenIcon,
  trendingup: TrendingUpIcon,
  twitter: TwitterIcon,
  user: UserIcon,
  users: UsersIcon,
  video: VideoIcon,
  youtube: YoutubeIcon,
}

export function PresetIcon({ name, className }: { name?: string; className?: string }) {
  const Icon = ICONS[(name ?? "").toLowerCase().replace(/[^a-z0-9]/g, "")] ?? WorkflowIcon
  return <Icon className={className} aria-hidden />
}
