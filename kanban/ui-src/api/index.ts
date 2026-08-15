import { api } from "./client";
import type { Project, Column, Session, Ticket, TicketWithSubtasks, CreateTicketInput, UpdateTicketInput, MoveTicketInput, CreateSubtaskInput, CreateSessionInput } from "@shared/types";

export const fetchProjects = () => api.get<Project[]>("list_projects");
export const createProject = (name: string) => api.post<Project>("create_project", { name });
export const fetchColumns = (project_id: string) => api.get<Column[]>("list_columns", { project_id });
export const fetchTickets = (filters?: Record<string, string>) => api.get<Ticket[]>("list_tickets", filters);
export const fetchTicket = (ticket_id: string) => api.get<TicketWithSubtasks>("get_ticket", { ticket_id });
export const createTicket = (input: CreateTicketInput) => api.post<Ticket>("create_ticket", input as unknown as Record<string, unknown>);
export const updateTicket = (ticket_id: string, input: UpdateTicketInput) => api.put<Ticket>("update_ticket", { ticket_id, ...input });
export const deleteTicket = (ticket_id: string) => api.del<{ deleted: boolean }>("delete_ticket", { ticket_id });
export const moveTicket = (ticket_id: string, input: MoveTicketInput) => api.put<Ticket>("move_ticket", { ticket_id, ...input });
export const createSubtask = (parent_ticket_id: string, input: CreateSubtaskInput) => api.post<Ticket>("create_subtask", { parent_ticket_id, ...input });
export const fetchSessions = () => api.get<Session[]>("list_sessions");
export const createSession = (input: CreateSessionInput) => api.post<Session>("create_session", input as unknown as Record<string, unknown>);
export const deleteSession = (session_id: string) => api.del<{ deleted: boolean }>("delete_session", { session_id });
