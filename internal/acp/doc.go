// Package acp contains Ox's Go representation of the ACP wire types it
// implements.
//
// Ox is a strict superset of ACP. Custom methods must start with an
// underscore, and custom data belongs only in ACP's _meta fields. Ox never
// adds custom fields to the root of an ACP-defined type.
package acp
