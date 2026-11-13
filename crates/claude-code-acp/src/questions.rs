//! Claude's `AskUserQuestion` questions as an elicitation form, and the form's
//! answers as the tool's input.
//!
//! Question `i`, counting from 1, becomes the property `q{i}`, a choice of
//! its options, and `q{i}-other`, a text field for an answer of the user's
//! own. No property is required, since a question answered in its text field
//! has no choice. The properties sort in question order, which is the order
//! clients show them in.

use std::collections::BTreeMap;

use agent_client_protocol::v2::schema::{
    ElicitationContentValue, ElicitationSchema, EnumOption, MultiSelectPropertySchema,
    StringPropertySchema,
};
use serde_json::{Map, Value, json};

/// A question of `AskUserQuestion`'s input.
struct Question<'a> {
    text: &'a str,
    header: Option<&'a str>,
    /// Each option's label and description.
    options: Vec<(&'a str, Option<&'a str>)>,
    multi_select: bool,
}

/// The questions of `AskUserQuestion`'s input, or `None` when it has none or
/// one lacks its text or a labelled option.
fn questions(input: &Value) -> Option<Vec<Question<'_>>> {
    let questions = input["questions"].as_array()?;
    if questions.is_empty() {
        return None;
    }
    questions
        .iter()
        .map(|question| {
            let options = question["options"].as_array()?;
            if options.is_empty() {
                return None;
            }
            Some(Question {
                text: question["question"].as_str()?,
                header: question["header"].as_str(),
                options: options
                    .iter()
                    .map(|option| Some((option["label"].as_str()?, option["description"].as_str())))
                    .collect::<Option<_>>()?,
                multi_select: question["multiSelect"].as_bool().unwrap_or(false),
            })
        })
        .collect()
}

/// The elicitation message and form that ask `AskUserQuestion`'s questions,
/// or `None` when the input does not hold questions the form can show.
pub fn form(input: &Value) -> Option<(String, ElicitationSchema)> {
    let questions = questions(input)?;
    let message = if questions.len() == 1 {
        "Claude has a question"
    } else {
        "Claude has questions"
    };
    let mut schema = ElicitationSchema::new();
    for (index, question) in questions.iter().enumerate() {
        let name = format!("q{}", index + 1);
        let options: Vec<EnumOption> = question
            .options
            .iter()
            .map(|(label, description)| {
                EnumOption::new(*label, *label).description(description.map(str::to_owned))
            })
            .collect();
        schema = if question.multi_select {
            schema.property(
                &name,
                MultiSelectPropertySchema::titled(options)
                    .title(question.header.map(str::to_owned))
                    .description(question.text),
                false,
            )
        } else {
            schema.property(
                &name,
                StringPropertySchema::new()
                    .title(question.header.map(str::to_owned))
                    .description(question.text)
                    .one_of(options),
                false,
            )
        };
        schema = schema.property(
            format!("{name}-other"),
            StringPropertySchema::new().title("Other"),
            false,
        );
    }
    Some((message.to_owned(), schema))
}

/// `AskUserQuestion`'s input with the answers in a form's `content`, keyed by
/// question text. A single-select question's answer is the text typed in its
/// `Other` field, or else its chosen label. A multi-select question's is its
/// chosen labels in option order, then its `Other` text, joined with `", "`.
/// A question with neither has no answer. Values of the wrong type and
/// labels that are not the question's options are ignored.
pub fn answers(input: &Value, content: &BTreeMap<String, ElicitationContentValue>) -> Value {
    let mut answers = Map::new();
    for (index, question) in questions(input).unwrap_or_default().iter().enumerate() {
        let name = format!("q{}", index + 1);
        let other = match content.get(&format!("{name}-other")) {
            Some(ElicitationContentValue::String(text)) if !text.trim().is_empty() => {
                Some(text.as_str())
            }
            _ => None,
        };
        let is_option = |label: &str| question.options.iter().any(|(option, _)| *option == label);
        let answer: Vec<&str> = match content.get(&name) {
            Some(ElicitationContentValue::StringArray(labels)) if question.multi_select => question
                .options
                .iter()
                .map(|(label, _)| *label)
                .filter(|label| labels.iter().any(|chosen| chosen == label))
                .chain(other)
                .collect(),
            Some(ElicitationContentValue::String(label))
                if !question.multi_select && other.is_none() && is_option(label) =>
            {
                vec![label.as_str()]
            }
            _ => other.into_iter().collect(),
        };
        if !answer.is_empty() {
            answers.insert(question.text.to_owned(), answer.join(", ").into());
        }
    }
    json!({"questions": input["questions"], "answers": answers})
}

#[cfg(test)]
mod tests {
    use super::*;

    fn input() -> Value {
        json!({"questions": [
            {
                "question": "How should I format the output?",
                "header": "Format",
                "options": [
                    {"label": "Summary", "description": "Brief overview"},
                    {"label": "Detailed", "description": "Full explanation"}
                ],
                "multiSelect": false
            },
            {
                "question": "Which sections should I include?",
                "header": "Sections",
                "options": [
                    {"label": "Introduction", "description": "Opening context"},
                    {"label": "Conclusion", "description": "Final summary"}
                ],
                "multiSelect": true
            }
        ]})
    }

    fn content(value: Value) -> BTreeMap<String, ElicitationContentValue> {
        serde_json::from_value(value).unwrap()
    }

    #[test]
    fn questions_become_a_choice_and_a_text_field_each() {
        let (message, schema) = form(&input()).unwrap();
        assert_eq!(message, "Claude has questions");
        assert_eq!(
            serde_json::to_value(&schema).unwrap(),
            json!({
                "type": "object",
                "properties": {
                    "q1": {
                        "type": "string",
                        "title": "Format",
                        "description": "How should I format the output?",
                        "oneOf": [
                            {"const": "Summary", "title": "Summary", "description": "Brief overview"},
                            {"const": "Detailed", "title": "Detailed", "description": "Full explanation"}
                        ]
                    },
                    "q1-other": {"type": "string", "title": "Other"},
                    "q2": {
                        "type": "array",
                        "title": "Sections",
                        "description": "Which sections should I include?",
                        "items": {"anyOf": [
                            {"const": "Introduction", "title": "Introduction", "description": "Opening context"},
                            {"const": "Conclusion", "title": "Conclusion", "description": "Final summary"}
                        ]}
                    },
                    "q2-other": {"type": "string", "title": "Other"}
                }
            })
        );
        let names: Vec<&String> = schema.properties.keys().collect();
        assert_eq!(names, ["q1", "q1-other", "q2", "q2-other"]);
    }

    #[test]
    fn one_question_has_a_singular_message() {
        let mut input = input();
        input["questions"].as_array_mut().unwrap().pop();
        assert_eq!(form(&input).unwrap().0, "Claude has a question");
    }

    #[test]
    fn input_without_questions_has_no_form() {
        for input in [
            json!({}),
            json!({"questions": []}),
            json!({"questions": "Which?"}),
            json!({"questions": [{"options": [{"label": "A"}]}]}),
            json!({"questions": [{"question": "Which?"}]}),
            json!({"questions": [{"question": "Which?", "options": []}]}),
            json!({"questions": [{"question": "Which?", "options": [{"description": "A"}]}]}),
        ] {
            assert!(form(&input).is_none(), "{input}");
        }
    }

    #[test]
    fn a_chosen_label_is_the_answer() {
        let answered = answers(&input(), &content(json!({"q1": "Summary"})));
        assert_eq!(answered["questions"], input()["questions"]);
        assert_eq!(
            answered["answers"],
            json!({"How should I format the output?": "Summary"})
        );
    }

    #[test]
    fn other_text_replaces_a_single_choice() {
        let answered = answers(
            &input(),
            &content(json!({"q1": "Summary", "q1-other": "Bullet points"})),
        );
        assert_eq!(
            answered["answers"],
            json!({"How should I format the output?": "Bullet points"})
        );
    }

    #[test]
    fn multiple_choices_and_other_text_join_in_option_order() {
        let answered = answers(
            &input(),
            &content(json!({"q2": ["Conclusion", "Introduction"], "q2-other": "Appendix"})),
        );
        assert_eq!(
            answered["answers"],
            json!({"Which sections should I include?": "Introduction, Conclusion, Appendix"})
        );
    }

    #[test]
    fn a_question_left_blank_has_no_answer() {
        let answered = answers(
            &input(),
            &content(json!({"q1-other": "  ", "q2": [], "q2-other": ""})),
        );
        assert_eq!(answered["answers"], json!({}));
    }

    #[test]
    fn unknown_labels_and_wrong_types_are_ignored() {
        let answered = answers(
            &input(),
            &content(json!({"q1": "Verbose", "q2": "Introduction", "q2-other": 3})),
        );
        assert_eq!(answered["answers"], json!({}));
        let answered = answers(&input(), &content(json!({"q2": ["Introduction", "Index"]})));
        assert_eq!(
            answered["answers"],
            json!({"Which sections should I include?": "Introduction"})
        );
    }
}
