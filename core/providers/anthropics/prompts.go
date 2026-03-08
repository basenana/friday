package anthropics

const DEFAULT_STRUCTURED_PREDICT_PROMPT = `You are a helpful assistant. Your task is to output a valid JSON object that matches the schema below.

Schema:
{insert_json_schema_here}

User request:
{insert_user_request_here}

Output only the JSON object, without any explanation or additional text.`
