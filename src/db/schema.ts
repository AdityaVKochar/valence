import { 
  pgTable, 
  uuid, 
  text, 
  boolean, 
  integer, 
  timestamp, 
  pgEnum,
  AnyPgColumn
} from 'drizzle-orm/pg-core';

export const difficultyEnum = pgEnum('difficulty', ['EASY', 'MEDIUM', 'HARD']);

export const languageEnum = pgEnum('language', [
  'C++',
  'JAVA',
  'PYTHON',
  'JAVASCRIPT',
  'GO'
]);

export const evaluationStatusEnum = pgEnum('evaluation_status', [
  'PENDING', 
  'ACCEPTED', 
  'WRONG_ANSWER', 
  'TIME_LIMIT_EXCEEDED', 
  'MEMORY_LIMIT_EXCEEDED', 
  'COMPILE_ERROR', 
  'RUNTIME_ERROR'
]);

export const user = pgTable('user', {
  id: text('id').primaryKey(),
  name: text('name').default('').notNull(),
  firstName: text('first_name'),
  lastName: text('last_name'),
  email: text('email').notNull().unique(),
  emailVerified: boolean('email_verified').default(false).notNull(),
  image: text('image'),
  isAdmin: boolean('is_admin').default(false).notNull(),
  createdAt: timestamp('created_at').defaultNow().notNull(),
  updatedAt: timestamp('updated_at').defaultNow().notNull(),
});

export const session = pgTable('session', {
  id: text('id').primaryKey(),
  expiresAt: timestamp('expires_at').notNull(),
  token: text('token').notNull().unique(),
  createdAt: timestamp('created_at').notNull(),
  updatedAt: timestamp('updated_at').notNull(),
  ipAddress: text('ip_address'),
  userAgent: text('user_agent'),
  userId: text('user_id').notNull().references(() => user.id, { onDelete: 'cascade' }), // text references text -> Valid!
});

export const account = pgTable('account', {
  id: text('id').primaryKey(),
  accountId: text('account_id').notNull(),
  providerId: text('provider_id').notNull(),
  userId: text('user_id').notNull().references(() => user.id, { onDelete: 'cascade' }), // text references text -> Valid!
  accessToken: text('access_token'),
  refreshToken: text('refresh_token'),
  idToken: text('id_token'),
  accessTokenExpiresAt: timestamp('access_token_expires_at'),
  refreshTokenExpiresAt: timestamp('refresh_token_expires_at'),
  scope: text('scope'),
  password: text('password'),
  createdAt: timestamp('created_at').notNull(),
  updatedAt: timestamp('updated_at').notNull(),
});

export const verification = pgTable('verification', {
  id: text('id').primaryKey(),
  identifier: text('identifier').notNull(),
  value: text('value').notNull(),
  expiresAt: timestamp('expires_at').notNull(),
  createdAt: timestamp('created_at'),
  updatedAt: timestamp('updated_at'),
});


export const questions = pgTable('questions', {
  id: uuid('id').defaultRandom().primaryKey(),
  title: text('title').notNull(),
  description: text('description').notNull(),
  difficulty: difficultyEnum('difficulty').notNull(),
  tags: text('tags').array().notNull().default([]),
  timeLimitMs: integer('time_limit_ms').notNull().default(2000),
  memoryLimitKb: integer('memory_limit_kb').notNull().default(512000),
  isHidden: boolean('is_hidden').default(false).notNull(),
});

export const testCases = pgTable('test_cases', {
  id: uuid('id').defaultRandom().primaryKey(),
  questionId: uuid('question_id').notNull().references(() => questions.id, { onDelete: 'cascade' }),
  input: text('input').notNull(),
  output: text('output').notNull(),
  isHidden: boolean('is_hidden').default(false).notNull(),
  orderIndex: integer('order_index').notNull().default(0),
});

export const submissions = pgTable('submissions', {
  id: uuid('id').defaultRandom().primaryKey(),
  userId: text('user_id').notNull().references(() => user.id, { onDelete: 'cascade' }), // Fix: Changed from uuid() to text() to match user.id
  teamId: uuid('team_id'), 
  questionId: uuid('question_id').notNull().references(() => questions.id, { onDelete: 'cascade' }),
  code: text('code').notNull(),
  language: languageEnum('language').notNull(),
  evaluated: boolean('evaluated').default(false).notNull(),
  testcasesPassed: integer('testcases_passed').default(0).notNull(),
  createdAt: timestamp('created_at').defaultNow().notNull(),
  updatedAt: timestamp('updated_at').defaultNow().notNull(),
});

export const submissionTestcases = pgTable('submission_testcases', {
  id: uuid('id').defaultRandom().primaryKey(),
  submissionId: uuid('submission_id').notNull().references(() => submissions.id, { onDelete: 'cascade' }),
  testcaseId: uuid('testcase_id').notNull().references(() => testCases.id, { onDelete: 'cascade' }),
  passed: boolean('passed').default(false).notNull(),
  evaluated: boolean('evaluated').default(false).notNull(),
  evaluationStatus: evaluationStatusEnum('evaluation_status').default('PENDING').notNull(),
});

export const hints = pgTable('hints', {
  id: uuid('id').defaultRandom().primaryKey(),
  questionId: uuid('question_id').notNull().references(() => questions.id, { onDelete: 'cascade' }),
  hintText: text('hint_text').notNull(),
  hintOrder: integer('hint_order').notNull().default(1),
  createdAt: timestamp('created_at').defaultNow().notNull(),
});

export const comments = pgTable('comments', {
  id: uuid('id').defaultRandom().primaryKey(),
  questionId: uuid('question_id').notNull().references(() => questions.id, { onDelete: 'cascade' }),
  userId: text('user_id').notNull().references(() => user.id, { onDelete: 'cascade' }), // Correctly typed as text
  content: text('content').notNull(),
  parentId: uuid('parent_id').references((): AnyPgColumn => comments.id, { onDelete: 'cascade' }), 
  createdAt: timestamp('created_at').defaultNow().notNull(),
  updatedAt: timestamp('updated_at').defaultNow().notNull(),
});