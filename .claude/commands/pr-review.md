# MR initial review 

## Objective
Fetch the changes of a merge request in github, and do a first review using the agent backend-code-reviewer

## Input Parameters
- Merge request ID 'REQUESTID' 
- Requirements 

## Review Steps
1. Fetch the merge request code, use gh to fetch the branch detached 'gh pr checkout <PR_NUMBER> --detach'
2. Ask for the number of commits to review
3. Use the agent backend-code-reviewer for the new changes this MR adds
