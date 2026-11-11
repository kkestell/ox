use agent_client_protocol::v1::schema::{ContentBlock, EmbeddedResourceResource};
use serde_json::{Value, json};

/// Image types Claude accepts.
const IMAGE_TYPES: [&str; 4] = ["image/png", "image/jpeg", "image/gif", "image/webp"];

/// Converts an ACP prompt into the content of a Claude user message.
///
/// Text passes through. Images become image blocks. A resource link becomes
/// a Markdown link Claude can follow with its tools. An embedded text
/// resource becomes a `<context>` block; an embedded image or PDF becomes an
/// image or document block.
pub fn claude_content(prompt: &[ContentBlock]) -> Result<Vec<Value>, String> {
    let empty = prompt.iter().all(|block| match block {
        ContentBlock::Text(text) => text.text.trim().is_empty(),
        _ => false,
    });
    if empty {
        return Err("Empty prompt".to_owned());
    }
    prompt
        .iter()
        .map(|block| match block {
            ContentBlock::Text(text) => Ok(text_block(&text.text)),
            ContentBlock::Image(image) => image_block(&image.mime_type, &image.data),
            ContentBlock::Audio(_) => Err("Audio prompts are not supported".to_owned()),
            ContentBlock::ResourceLink(link) => {
                Ok(text_block(&format!("[@{}]({})", link.name, link.uri)))
            }
            ContentBlock::Resource(resource) => match &resource.resource {
                EmbeddedResourceResource::TextResourceContents(text) => Ok(text_block(&format!(
                    "<context ref=\"{}\">\n{}\n</context>",
                    text.uri, text.text
                ))),
                EmbeddedResourceResource::BlobResourceContents(blob) => {
                    match blob.mime_type.as_deref() {
                        Some("application/pdf") => Ok(json!({
                            "type": "document",
                            "source": {
                                "type": "base64",
                                "media_type": "application/pdf",
                                "data": blob.blob,
                            },
                        })),
                        Some(mime_type) if mime_type.starts_with("image/") => {
                            image_block(mime_type, &blob.blob)
                        }
                        mime_type => Err(format!(
                            "Unsupported resource type {} for {}",
                            mime_type.unwrap_or("(none)"),
                            blob.uri
                        )),
                    }
                }
                _ => Err("Unsupported resource".to_owned()),
            },
            _ => Err("Unsupported content block".to_owned()),
        })
        .collect()
}

/// The text of a prompt's text blocks, for its session title.
pub fn prompt_text(prompt: &[ContentBlock]) -> String {
    prompt
        .iter()
        .filter_map(|block| match block {
            ContentBlock::Text(text) => Some(text.text.as_str()),
            _ => None,
        })
        .collect::<Vec<_>>()
        .join("\n")
}

fn text_block(text: &str) -> Value {
    json!({"type": "text", "text": text})
}

fn image_block(mime_type: &str, data: &str) -> Result<Value, String> {
    if !IMAGE_TYPES.contains(&mime_type) {
        return Err(format!("Unsupported image type {mime_type}"));
    }
    Ok(json!({
        "type": "image",
        "source": {"type": "base64", "media_type": mime_type, "data": data},
    }))
}
